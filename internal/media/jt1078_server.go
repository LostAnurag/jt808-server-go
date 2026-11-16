package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
	"github.com/fakeyanss/jt808-server-go/pkg/routines"
)

// StreamKey generates a unique key for a device phone and logical channel.
func StreamKey(phone string, channel uint8) string {
	return fmt.Sprintf("%s_%d", phone, channel)
}

// StreamBroadcaster manages subscribers for a specific live stream.
type StreamBroadcaster struct {
	mu              sync.RWMutex
	subscribers     map[chan []byte]struct{}
	sps             []byte
	pps             []byte
	seqHeaderTag    []byte
	lastKeyframeTag []byte
	lastTag         []byte
	lastPacketAt    time.Time
	packetCount     uint64
	frameCount      uint64
	dts             uint32
}

// MediaHub coordinates all live video streams and subscriber connections.
type MediaHub struct {
	mu      sync.RWMutex
	streams map[string]*StreamBroadcaster
}

var globalHub = &MediaHub{
	streams: make(map[string]*StreamBroadcaster),
}

// GetMediaHub returns the singleton MediaHub instance.
func GetMediaHub() *MediaHub {
	return globalHub
}

func (h *MediaHub) getOrCreate(streamKey string) *StreamBroadcaster {
	h.mu.Lock()
	defer h.mu.Unlock()

	b, ok := h.streams[streamKey]
	if !ok {
		b = &StreamBroadcaster{
			subscribers: make(map[chan []byte]struct{}),
		}
		h.streams[streamKey] = b
	}
	return b
}

// Subscribe attaches a new subscriber channel to the given stream.
func (h *MediaHub) Subscribe(streamKey string) (chan []byte, []byte, []byte, func()) {
	b := h.getOrCreate(streamKey)
	ch := make(chan []byte, 256)

	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	seqTag := b.seqHeaderTag
	kfTag := b.lastKeyframeTag
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		delete(b.subscribers, ch)
		close(ch)
		b.mu.Unlock()
	}

	return ch, seqTag, kfTag, unsub
}

// GetSeqHeaderTag returns a copy of the cached AVC sequence header tag for the stream, if available.
func (h *MediaHub) GetSeqHeaderTag(streamKey string) []byte {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b, ok := h.streams[streamKey]
	if !ok {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.seqHeaderTag) == 0 {
		return nil
	}
	res := make([]byte, len(b.seqHeaderTag))
	copy(res, b.seqHeaderTag)
	return res
}

// Broadcast distributes an FLV tag to all active subscribers of streamKey.
func (h *MediaHub) Broadcast(streamKey string, tag []byte, isKeyframe bool, sps, pps []byte) {
	b := h.getOrCreate(streamKey)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.packetCount++
	b.lastPacketAt = time.Now()
	b.lastTag = tag

	var newSeqHeader []byte
	if len(sps) > 0 && len(pps) > 0 {
		if !bytes.Equal(b.sps, sps) || !bytes.Equal(b.pps, pps) {
			b.sps = sps
			b.pps = pps
			b.seqHeaderTag = MakeAVCSequenceHeaderTag(sps, pps, 0)
			newSeqHeader = b.seqHeaderTag
		}
	}

	if isKeyframe {
		b.lastKeyframeTag = tag
	}

	for ch := range b.subscribers {
		if len(newSeqHeader) > 0 {
			select {
			case ch <- newSeqHeader:
			default:
			}
		}
		select {
		case ch <- tag:
		default:
			// Drop frame if subscriber buffer is full to prevent backpressure
		}
	}
}

// HasActiveStream returns true if the stream has received packets within the last 15 seconds.
func (h *MediaHub) HasActiveStream(streamKey string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	b, ok := h.streams[streamKey]
	if !ok {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	return time.Since(b.lastPacketAt) < 15*time.Second
}

// FrameAssembler buffers and reassembles fragmented JT1078 RTP sub-packets.
// JT1078 Table 19 defines sub-packet indicators (low 4 bits of byte 15):
// 0000 = Atomic (single complete frame)
// 0001 = First sub-packet of a multi-packet frame
// 0010 = Last sub-packet of a multi-packet frame
// 0011 = Intermediate sub-packet
type FrameAssembler struct {
	mu      sync.Mutex
	buffers map[string]*frameBuffer
}

type frameBuffer struct {
	isKeyframe bool
	data       []byte
}

func newFrameAssembler() *FrameAssembler {
	return &FrameAssembler{
		buffers: make(map[string]*frameBuffer),
	}
}

func (fa *FrameAssembler) Assemble(streamKey string, subPacket byte, isKeyframe bool, payload []byte) (completeData []byte, isFullKeyframe bool) {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	buf, ok := fa.buffers[streamKey]
	if !ok {
		buf = &frameBuffer{data: make([]byte, 0, 65536)}
		fa.buffers[streamKey] = buf
	}

	switch subPacket {
	case 0: // Atomic packet
		buf.data = buf.data[:0]
		return payload, isKeyframe

	case 1: // First sub-packet of multi-packet frame
		buf.data = append(buf.data[:0], payload...)
		buf.isKeyframe = isKeyframe
		return nil, false

	case 3: // Intermediate sub-packet
		if len(buf.data) > 0 {
			if len(buf.data)+len(payload) > 1024*1024 {
				buf.data = buf.data[:0] // Drop on buffer overflow
				return nil, false
			}
			buf.data = append(buf.data, payload...)
		}
		return nil, false

	case 2: // Last sub-packet
		if len(buf.data) > 0 {
			buf.data = append(buf.data, payload...)
			res := make([]byte, len(buf.data))
			copy(res, buf.data)
			kf := buf.isKeyframe || isKeyframe
			buf.data = buf.data[:0]
			return res, kf
		}
		return nil, false // Discard orphaned tail packet if first packet was lost

	default:
		return payload, isKeyframe
	}
}

// JT1078Server represents the JT/T 1078 real-time video RTP ingestion server.
type JT1078Server struct {
	tcpPort   string
	udpPort   string
	listener  net.Listener
	udpConn   *net.UDPConn
	hub       *MediaHub
	assembler *FrameAssembler

	dtsMu  sync.Mutex
	streamDTS map[string]uint32
	cachedSPS map[string][]byte
	cachedPPS map[string][]byte
}

// NewJT1078Server creates a new JT1078 media server instance.
func NewJT1078Server(tcpPort, udpPort string) *JT1078Server {
	if tcpPort == "" {
		tcpPort = "1078"
	}
	if udpPort == "" {
		udpPort = "1078"
	}
	return &JT1078Server{
		tcpPort:   tcpPort,
		udpPort:   udpPort,
		hub:       GetMediaHub(),
		assembler: newFrameAssembler(),
		streamDTS: make(map[string]uint32),
		cachedSPS: make(map[string][]byte),
		cachedPPS: make(map[string][]byte),
	}
}

// Start begins listening for JT1078 TCP and UDP connections on the configured port.
func (s *JT1078Server) Start() error {
	addr := ":" + s.tcpPort
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error().Err(err).Str("addr", addr).Msg("Failed to start JT1078 RTP TCP listener")
		return err
	}
	s.listener = ln
	log.Info().Str("addr", addr).Msg("JT1078 RTP Media Server listening on TCP port")

	// Start UDP Listener on the same port
	udpAddr, err := net.ResolveUDPAddr("udp", ":"+s.udpPort)
	if err == nil {
		uConn, err := net.ListenUDP("udp", udpAddr)
		if err == nil {
			s.udpConn = uConn
			log.Info().Str("addr", ":"+s.udpPort).Msg("JT1078 RTP Media Server listening on UDP port")
			routines.GoSafe(func() { s.handleUDP() })
		}
	}

	routines.GoSafe(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				log.Warn().Err(err).Msg("Error accepting JT1078 TCP connection")
				continue
			}
			routines.GoSafe(func() {
				s.handleTCPConnection(conn)
			})
		}
	})

	return nil
}

// Close stops the server listeners.
func (s *JT1078Server) Close() error {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.udpConn != nil {
		_ = s.udpConn.Close()
	}
	return nil
}

// handleUDP processes incoming JT1078 RTP datagrams.
func (s *JT1078Server) handleUDP() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n >= 30 {
			log.Debug().Str("remote", addr.String()).Int("len", n).Msg("JT1078 UDP packet received")
			s.processSinglePacket(buf[:n])
		}
	}
}

// handleTCPConnection processes incoming stream of JT1078 RTP packets.
func (s *JT1078Server) handleTCPConnection(conn net.Conn) {
	defer conn.Close()
	remoteAddr := conn.RemoteAddr().String()
	log.Info().Str("remote", remoteAddr).Msg("New JT1078 RTP video stream connected")

	buf := make([]byte, 65536)
	streamBuf := make([]byte, 0, 262144)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Debug().Err(err).Str("remote", remoteAddr).Msg("JT1078 TCP connection closed")
			}
			break
		}
		if n == 0 {
			continue
		}

		streamBuf = append(streamBuf, buf[:n]...)

		// Scan for JT1078 sync packet header: 0x30 0x31 0x63 0x64 ("01cd")
		for len(streamBuf) >= 30 {
			idx := -1
			for i := 0; i <= len(streamBuf)-4; i++ {
				if streamBuf[i] == 0x30 && streamBuf[i+1] == 0x31 && streamBuf[i+2] == 0x63 && streamBuf[i+3] == 0x64 {
					idx = i
					break
				}
			}

			if idx < 0 {
				if len(streamBuf) > 3 {
					streamBuf = streamBuf[len(streamBuf)-3:]
				}
				break
			}

			if idx > 0 {
				streamBuf = streamBuf[idx:]
			}

			if len(streamBuf) < 30 {
				break
			}

			// Standard JT1078 Video frame (I, P, B frame): Header length is 30 bytes, body length offset is 28.
			// JT/T 1078 Table 19:
			// Bytes 0..3: 0x30 0x31 0x63 0x64
			// Bytes 15: DataType (high 4 bits) & SubPacket (low 4 bits)
			// Bytes 24..25: Last I-frame interval (2 bytes)
			// Bytes 26..27: Previous frame interval (2 bytes)
			// Bytes 28..29: Body Length (2 bytes)
			headerLen := 30
			bodyLenOffset := 28

			dataType := (streamBuf[15] >> 4) & 0x0F
			if dataType == 3 {
				// Audio frame: no interval fields (26 bytes header)
				headerLen = 26
				bodyLenOffset = 24
			} else if dataType == 4 {
				// Transparent data: no timestamp/intervals (18 bytes header)
				headerLen = 18
				bodyLenOffset = 16
			} else if len(streamBuf) >= 34 {
				// Non-standard vendor fallback check: if NALU start code is at offset 28
				if streamBuf[28] == 0 && streamBuf[29] == 0 && (streamBuf[30] == 1 || (streamBuf[30] == 0 && streamBuf[31] == 1)) {
					headerLen = 28
					bodyLenOffset = 26
				}
			}

			if len(streamBuf) < headerLen {
				break
			}

			bodyLen := int(binary.BigEndian.Uint16(streamBuf[bodyLenOffset : bodyLenOffset+2]))
			totalPacketLen := headerLen + bodyLen
			if len(streamBuf) < totalPacketLen {
				// Wait for remainder of packet data
				break
			}

			packet := streamBuf[:totalPacketLen]
			s.dispatchParsedPacket(packet, headerLen)
			streamBuf = streamBuf[totalPacketLen:]
		}
	}
}

// processSinglePacket processes a single standalone JT1078 datagram (UDP or extracted chunk).
func (s *JT1078Server) processSinglePacket(packet []byte) {
	if len(packet) < 30 {
		return
	}
	if packet[0] != 0x30 || packet[1] != 0x31 || packet[2] != 0x63 || packet[3] != 0x64 {
		return
	}

	headerLen := 30
	dataType := (packet[15] >> 4) & 0x0F
	if dataType == 3 {
		headerLen = 26
	} else if dataType == 4 {
		headerLen = 18
	} else if len(packet) >= 34 {
		if packet[28] == 0 && packet[29] == 0 && (packet[30] == 1 || (packet[30] == 0 && packet[31] == 1)) {
			headerLen = 28
		}
	}
	s.dispatchParsedPacket(packet, headerLen)
}

// dispatchParsedPacket decodes phone, channel, reassembles frame slices, and muxes to FLV.
func (s *JT1078Server) dispatchParsedPacket(packet []byte, headerLen int) {
	if len(packet) <= headerLen {
		return
	}

	phoneBytes := packet[8:14]
	phoneIdx := 0
	rawPhone := hex.ReadBCD(phoneBytes, &phoneIdx, 6)
	cleanPhone := strings.TrimLeft(rawPhone, "0")
	channel := packet[14]

	flagByte := packet[15]
	dataType := (flagByte >> 4) & 0x0F
	subPacket := flagByte & 0x0F

	dataTypeDesc := map[byte]string{
		0: "I-frame", 1: "P-frame", 2: "B-frame",
		3: "Audio", 4: "Transparent",
	}
	subPacketDesc := map[byte]string{
		0: "Atomic", 1: "First", 2: "Last", 3: "Middle",
	}

	var ts uint64
	if len(packet) >= 24 {
		for i := 16; i < 24; i++ {
			ts = ts<<8 | uint64(packet[i])
		}
	}
	serial := uint16(packet[6])<<8 | uint16(packet[7])
	bodyLen := len(packet) - headerLen

	log.Debug().
		Str("phone", cleanPhone).
		Uint8("channel", channel).
		Str("dataType", dataTypeDesc[dataType]).
		Str("subPacket", subPacketDesc[subPacket]).
		Uint16("serial", serial).
		Uint64("timestamp", ts).
		Int("bodyLen", bodyLen).
		Hex("header", packet[:headerLen]).
		Msg("JT1078 packet received")

	payload := packet[headerLen:]

	// Video payloads only (0=I-frame, 1=P-frame, 2=B-frame)
	if dataType > 2 {
		return
	}

	isKeyframe := (dataType == 0)
	streamKey := StreamKey(cleanPhone, channel)

	// Reassemble fragmented multi-packet frames
	completePayload, isCompleteKF := s.assembler.Assemble(streamKey, subPacket, isKeyframe, payload)
	if len(completePayload) == 0 {
		return // Waiting for remaining sub-packets of this frame
	}

	nalus := ExtractNALUs(completePayload)
	if len(nalus) == 0 {
		return
	}

	s.dtsMu.Lock()
	var sps = s.cachedSPS[streamKey]
	var pps = s.cachedPPS[streamKey]

	for _, n := range nalus {
		nt := GetNALUType(n)
		if nt == NALUTypeSPS {
			sps = n
			s.cachedSPS[streamKey] = n
		} else if nt == NALUTypePPS {
			pps = n
			s.cachedPPS[streamKey] = n
		} else if nt == NALUTypeIDR {
			isCompleteKF = true
		}
	}

	dts := s.streamDTS[streamKey] + 40 // ~25 fps increment (40 ms)
	s.streamDTS[streamKey] = dts
	s.dtsMu.Unlock()

	flvPayload := FormatNALUsForFLV(nalus)
	tag := MakeVideoTag(isCompleteKF, false, dts, flvPayload)

	// Broadcast to both clean and raw phone stream keys
	s.hub.Broadcast(StreamKey(cleanPhone, channel), tag, isCompleteKF, sps, pps)
	if rawPhone != cleanPhone {
		s.hub.Broadcast(StreamKey(rawPhone, channel), tag, isCompleteKF, sps, pps)
	}
}

// HandleHTTPFLV serves live HTTP-FLV streams to web browsers and video players.
// Endpoint: GET /live/:stream (e.g. /live/377077047531_1.flv or /live/test.flv)
func HandleHTTPFLV(c *gin.Context) {
	streamParam := c.Param("stream")
	streamKey := strings.TrimSuffix(streamParam, ".flv")
	isTest := c.Query("test") == "true" || strings.HasPrefix(streamKey, "test")

	c.Header("Content-Type", "video/x-flv")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// 1. Write standard FLV Header (13 bytes)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write(FLVHeader(false))
	if err != nil {
		return
	}
	c.Writer.Flush()

	hub := GetMediaHub()

	// If explicit test mode requested, stream synthetic AVC pattern
	if isTest {
		streamTestPattern(c)
		return
	}

	// 2. Subscribe to live stream from MediaHub
	tagChan, seqHeaderTag, lastKeyframeTag, unsubscribe := hub.Subscribe(streamKey)
	defer unsubscribe()

	var sentSeqHeader bool
	var baseDTS uint32
	var hasBaseDTS bool
	var waitingForKeyframe = true

	// If we already have cached SPS/PPS, send AVC Sequence Header tag immediately
	if len(seqHeaderTag) > 0 {
		_, _ = c.Writer.Write(seqHeaderTag)
		c.Writer.Flush()
		sentSeqHeader = true
	}

	// If we have a cached keyframe, send it immediately so player renders instantly
	if sentSeqHeader && len(lastKeyframeTag) > 0 {
		kfTag := make([]byte, len(lastKeyframeTag))
		copy(kfTag, lastKeyframeTag)
		SetTagDTS(kfTag, 0)
		_, _ = c.Writer.Write(kfTag)
		c.Writer.Flush()
		waitingForKeyframe = false
		hasBaseDTS = true
		baseDTS = GetTagDTS(lastKeyframeTag)
	}

	clientGone := c.Request.Context().Done()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case <-clientGone:
			return
		case tag, ok := <-tagChan:
			if !ok {
				return
			}
			if !timeout.Stop() {
				select {
				case <-timeout.C:
				default:
				}
			}
			timeout.Reset(15 * time.Second)

			// Sequence Header (AVCPacketType == 0)
			if IsSequenceHeaderTag(tag) {
				_, _ = c.Writer.Write(tag)
				c.Writer.Flush()
				sentSeqHeader = true
				continue
			}

			// Ensure Sequence Header was sent before any video frames
			if !sentSeqHeader {
				hubSeqTag := hub.GetSeqHeaderTag(streamKey)
				if len(hubSeqTag) > 0 {
					_, _ = c.Writer.Write(hubSeqTag)
					c.Writer.Flush()
					sentSeqHeader = true
				} else {
					continue
				}
			}

			// Wait for Keyframe if not yet synchronized
			isKF := IsKeyframeTag(tag)
			if waitingForKeyframe {
				if !isKF {
					continue // Discard P-frames until the first I-frame arrives
				}
				waitingForKeyframe = false
			}

			// Normalize DTS so stream starts at 0 for this client
			tagDTS := GetTagDTS(tag)
			if !hasBaseDTS {
				baseDTS = tagDTS
				hasBaseDTS = true
			}

			var clientDTS uint32
			if tagDTS >= baseDTS {
				clientDTS = tagDTS - baseDTS
			}

			outTag := make([]byte, len(tag))
			copy(outTag, tag)
			SetTagDTS(outTag, clientDTS)

			_, err := c.Writer.Write(outTag)
			if err != nil {
				return
			}
			c.Writer.Flush()

		case <-timeout.C:
			// No data received within timeout
			return
		}
	}
}

// streamTestPattern generates a continuous, standard-compliant H.264 FLV stream
// (128x96 color-cycling frames at 25 fps) to allow immediate browser verification.
func streamTestPattern(c *gin.Context) {
	seqTag := MakeAVCSequenceHeaderTag(TestSPS, TestPPS, 0)
	if len(seqTag) > 0 {
		_, _ = c.Writer.Write(seqTag)
		c.Writer.Flush()
	}

	ticker := time.NewTicker(40 * time.Millisecond) // 25 fps
	defer ticker.Stop()

	clientGone := c.Request.Context().Done()
	frameIdx := 0
	var dts uint32

	for {
		select {
		case <-clientGone:
			return
		case <-ticker.C:
			dts += 40
			isKeyframe := (frameIdx % 25 == 0)

			var slice []byte
			if isKeyframe {
				slice = TestIDRSlice
			} else {
				slice = TestPSlice
			}

			flvPayload := FormatNALUsForFLV([][]byte{slice})
			tag := MakeVideoTag(isKeyframe, false, dts, flvPayload)

			_, err := c.Writer.Write(tag)
			if err != nil {
				return
			}
			c.Writer.Flush()
			frameIdx++
		}
	}
}
