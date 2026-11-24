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
		b = &StreamBroadcaster{subscribers: make(map[chan []byte]struct{})}
		h.streams[streamKey] = b
	}
	return b
}

// Subscribe attaches a new subscriber channel to the given stream.
// Returns the channel, cached sequence header tag, cached last keyframe tag, and an unsubscribe func.
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

// GetSeqHeaderTag returns a copy of the cached AVC sequence header tag for the stream.
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
		}
	}
}

// HasActiveStream returns true if the stream received a keyframe within the last 15 seconds.
func (h *MediaHub) HasActiveStream(streamKey string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b, ok := h.streams[streamKey]
	if !ok {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.lastKeyframeTag) > 0 && time.Since(b.lastPacketAt) < 15*time.Second
}

// FrameAssembler reassembles fragmented JT1078 sub-packets into complete frames.
// JT/T 1078 sub-packet indicator (low 4 bits of byte 15):
//
//	0 = atomic (complete frame)
//	1 = first sub-packet
//	2 = last sub-packet
//	3 = intermediate sub-packet
type FrameAssembler struct {
	mu      sync.Mutex
	buffers map[string]*frameBuffer
}

type frameBuffer struct {
	isKeyframe bool
	data       []byte
}

func newFrameAssembler() *FrameAssembler {
	return &FrameAssembler{buffers: make(map[string]*frameBuffer)}
}

func (fa *FrameAssembler) Assemble(streamKey string, subPacket byte, isKeyframe bool, payload []byte) ([]byte, bool) {
	fa.mu.Lock()
	defer fa.mu.Unlock()

	buf, ok := fa.buffers[streamKey]
	if !ok {
		buf = &frameBuffer{data: make([]byte, 0, 65536)}
		fa.buffers[streamKey] = buf
	}

	switch subPacket {
	case 0: // atomic
		return payload, isKeyframe
	case 1: // first
		buf.data = append(buf.data[:0], payload...)
		buf.isKeyframe = isKeyframe
		return nil, false
	case 3: // middle
		if len(buf.data) > 0 {
			if len(buf.data)+len(payload) > 1024*1024 {
				buf.data = buf.data[:0]
				return nil, false
			}
			buf.data = append(buf.data, payload...)
		}
		return nil, false
	case 2: // last
		if len(buf.data) > 0 {
			buf.data = append(buf.data, payload...)
			res := make([]byte, len(buf.data))
			copy(res, buf.data)
			kf := buf.isKeyframe || isKeyframe
			buf.data = buf.data[:0]
			return res, kf
		}
		return nil, false
	default:
		return payload, isKeyframe
	}
}

// JT1078Server is the JT/T 1078 RTP media ingestion server.
type JT1078Server struct {
	tcpPort    string
	udpPort    string
	listener   net.Listener
	udpConn    *net.UDPConn
	hub        *MediaHub
	assembler  *FrameAssembler
	dtsMu      sync.Mutex
	streamDTS  map[string]uint32
	cachedSPS  map[string][]byte
	cachedPPS  map[string][]byte
	dedupMu    sync.Mutex
	lastSerial map[string]uint16
}

func NewJT1078Server(tcpPort, udpPort string) *JT1078Server {
	if tcpPort == "" {
		tcpPort = "1078"
	}
	if udpPort == "" {
		udpPort = "1078"
	}
	return &JT1078Server{
		tcpPort:    tcpPort,
		udpPort:    udpPort,
		hub:        GetMediaHub(),
		assembler:  newFrameAssembler(),
		streamDTS:  make(map[string]uint32),
		cachedSPS:  make(map[string][]byte),
		cachedPPS:  make(map[string][]byte),
		lastSerial: make(map[string]uint16),
	}
}

func (s *JT1078Server) Start() error {
	addr := ":" + s.tcpPort
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error().Err(err).Str("addr", addr).Msg("Failed to start JT1078 TCP listener")
		return err
	}
	s.listener = ln
	log.Info().Str("addr", addr).Msg("JT1078 RTP Media Server listening on TCP")

	udpAddr, err := net.ResolveUDPAddr("udp", ":"+s.udpPort)
	if err == nil {
		if uConn, err := net.ListenUDP("udp", udpAddr); err == nil {
			s.udpConn = uConn
			log.Info().Str("addr", ":"+s.udpPort).Msg("JT1078 RTP Media Server listening on UDP")
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
				log.Warn().Err(err).Msg("JT1078 TCP accept error")
				continue
			}
			routines.GoSafe(func() { s.handleTCPConn(conn) })
		}
	})
	return nil
}

func (s *JT1078Server) Close() error {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.udpConn != nil {
		_ = s.udpConn.Close()
	}
	return nil
}

func (s *JT1078Server) handleUDP() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n >= 16 {
			log.Debug().Str("remote", addr.String()).Int("len", n).Msg("JT1078 UDP packet received")
			s.dispatchPacket(buf[:n])
		}
	}
}

// JT/T 1078-2016 Table 19 — RTP header layout:
//
// Common prefix (16 bytes):
//   [0-3]  sync word: 0x30 0x31 0x63 0x64
//   [4]    simulation type
//   [5]    version
//   [6-7]  sequence number (uint16 BE)
//   [8-13] SIM card number (6 bytes BCD)
//   [14]   logical channel number
//   [15]   data type (high 4 bits) | sub-packet (low 4 bits)
//
// Video frame (dataType 0/1/2 — I/P/B):
//   [16-19] timestamp (uint32 BE, milliseconds)
//   [20-23] last I-frame interval (uint32 BE)
//   [24-25] last frame interval (uint16 BE)
//   [26-27] body length (uint16 BE)   → total header = 28 bytes
//
// Audio frame (dataType 3):
//   [16-19] timestamp (uint32 BE, ms)
//   [20-21] body length (uint16 BE)   → total header = 22 bytes
//
// Transparent data (dataType 4):
//   [16-17] body length (uint16 BE)   → total header = 18 bytes
func parseHeader(buf []byte) (headerLen, bodyLen int, ok bool) {
	if len(buf) < 16 {
		return 0, 0, false
	}
	dt := (buf[15] >> 4) & 0x0F
	switch dt {
	case 0, 1, 2:
		if len(buf) < 28 {
			return 0, 0, false
		}
		return 28, int(binary.BigEndian.Uint16(buf[26:28])), true
	case 3:
		if len(buf) < 22 {
			return 0, 0, false
		}
		return 22, int(binary.BigEndian.Uint16(buf[20:22])), true
	case 4:
		if len(buf) < 18 {
			return 0, 0, false
		}
		return 18, int(binary.BigEndian.Uint16(buf[16:18])), true
	default:
		return 0, 0, false
	}
}

func (s *JT1078Server) handleTCPConn(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	log.Info().Str("remote", remote).Msg("New JT1078 TCP connection")

	buf := make([]byte, 65536)
	stream := make([]byte, 0, 262144)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Debug().Err(err).Str("remote", remote).Msg("JT1078 TCP connection closed")
			}
			break
		}
		if n == 0 {
			continue
		}
		stream = append(stream, buf[:n]...)

		for len(stream) >= 16 {
			// Find sync word
			idx := -1
			for i := 0; i <= len(stream)-4; i++ {
				if stream[i] == 0x30 && stream[i+1] == 0x31 && stream[i+2] == 0x63 && stream[i+3] == 0x64 {
					idx = i
					break
				}
			}
			if idx < 0 {
				if len(stream) > 3 {
					stream = stream[len(stream)-3:]
				}
				break
			}
			if idx > 0 {
				stream = stream[idx:]
			}

			hLen, bLen, ok := parseHeader(stream)
			if !ok {
				break // need more data
			}
			total := hLen + bLen
			if len(stream) < total {
				break // need more data
			}

			s.dispatchPacket(stream[:total])
			stream = stream[total:]
		}
	}
}

// dispatchPacket validates sync word, parses header, and routes to dispatchParsedPacket.
func (s *JT1078Server) dispatchPacket(packet []byte) {
	if len(packet) < 16 {
		return
	}
	if packet[0] != 0x30 || packet[1] != 0x31 || packet[2] != 0x63 || packet[3] != 0x64 {
		return
	}
	hLen, _, ok := parseHeader(packet)
	if !ok {
		return
	}
	s.dispatchParsedPacket(packet, hLen)
}

var (
	dtDesc = map[byte]string{0: "I-frame", 1: "P-frame", 2: "B-frame", 3: "Audio", 4: "Transparent"}
	spDesc = map[byte]string{0: "Atomic", 1: "First", 2: "Last", 3: "Middle"}
)

func (s *JT1078Server) dispatchParsedPacket(packet []byte, headerLen int) {
	if len(packet) <= headerLen {
		return
	}

	// Parse phone from bytes 8-13 (6-byte BCD)
	phoneIdx := 0
	rawPhone := hex.ReadBCD(packet[8:14], &phoneIdx, 6)
	cleanPhone := strings.TrimLeft(rawPhone, "0")
	channel := packet[14]
	flagByte := packet[15]
	dataType := (flagByte >> 4) & 0x0F
	subPacket := flagByte & 0x0F
	serial := binary.BigEndian.Uint16(packet[6:8])

	// Timestamp: bytes 16-19 (uint32 BE, milliseconds) — present for video and audio
	var tsMs uint32
	if headerLen >= 22 {
		tsMs = binary.BigEndian.Uint32(packet[16:20])
	}

	log.Debug().
		Str("phone", cleanPhone).
		Uint8("channel", channel).
		Str("dataType", dtDesc[dataType]).
		Str("subPacket", spDesc[subPacket]).
		Uint16("serial", serial).
		Uint32("tsMs", tsMs).
		Int("bodyLen", len(packet)-headerLen).
		Hex("header", packet[:headerLen]).
		Msg("JT1078 packet")

	// Only process video frames (I=0, P=1, B=2)
	if dataType > 2 {
		return
	}

	isKeyframe := dataType == 0
	streamKey := StreamKey(cleanPhone, channel)

	// Dedup: drop frames with same serial already processed (duplicate TCP connections)
	s.dedupMu.Lock()
	if last, seen := s.lastSerial[streamKey]; seen && serial == last && subPacket != 1 {
		s.dedupMu.Unlock()
		return
	}
	if subPacket == 0 || subPacket == 1 {
		s.lastSerial[streamKey] = serial
	}
	s.dedupMu.Unlock()

	payload := packet[headerLen:]

	completePayload, isCompleteKF := s.assembler.Assemble(streamKey, subPacket, isKeyframe, payload)
	if len(completePayload) == 0 {
		return
	}

	nalus := ExtractNALUs(completePayload)
	if len(nalus) == 0 {
		return
	}

	s.dtsMu.Lock()
	sps := s.cachedSPS[streamKey]
	pps := s.cachedPPS[streamKey]

	for _, n := range nalus {
		switch GetNALUType(n) {
		case NALUTypeSPS:
			sps = n
			s.cachedSPS[streamKey] = n
		case NALUTypePPS:
			pps = n
			s.cachedPPS[streamKey] = n
		case NALUTypeIDR:
			isCompleteKF = true
		}
	}

	// Compute DTS relative to stream start (timestamp is already in ms)
	baseKey := streamKey + "_base"
	base, hasBase := s.streamDTS[baseKey]
	var dts uint32
	if !hasBase || base == 0 {
		s.streamDTS[baseKey] = tsMs
		dts = 0
	} else if tsMs >= base {
		dts = tsMs - base
	} else {
		// Timestamp reset/wrap — rebase
		s.streamDTS[baseKey] = tsMs
		dts = 0
	}
	s.dtsMu.Unlock()

	if isCompleteKF && (len(sps) == 0 || len(pps) == 0) {
		log.Warn().Str("streamKey", streamKey).Msg("I-frame received but SPS/PPS not yet cached, dropping")
		return
	}

	flvPayload := FormatNALUsForFLV(nalus)
	tag := MakeVideoTag(isCompleteKF, false, dts, flvPayload)

	s.hub.Broadcast(StreamKey(cleanPhone, channel), tag, isCompleteKF, sps, pps)
	if rawPhone != cleanPhone {
		s.hub.Broadcast(StreamKey(rawPhone, channel), tag, isCompleteKF, sps, pps)
	}
}

// HandleHTTPFLV serves live HTTP-FLV streams.
// GET /live/:stream  e.g. /live/377077047531_1.flv  or  /live/test.flv?test=true
func HandleHTTPFLV(c *gin.Context) {
	streamParam := c.Param("stream")
	streamKey := strings.TrimSuffix(streamParam, ".flv")
	isTest := c.Query("test") == "true" || strings.HasPrefix(streamKey, "test")

	c.Header("Content-Type", "video/x-flv")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(FLVHeader(false)); err != nil {
		return
	}
	c.Writer.Flush()

	if isTest {
		streamTestPattern(c)
		return
	}

	hub := GetMediaHub()
	tagChan, seqHeaderTag, lastKeyframeTag, unsubscribe := hub.Subscribe(streamKey)
	defer unsubscribe()

	var sentSeqHeader bool
	var baseDTS uint32
	var hasBaseDTS bool
	waitingForKeyframe := true

	if len(seqHeaderTag) > 0 {
		_, _ = c.Writer.Write(seqHeaderTag)
		c.Writer.Flush()
		sentSeqHeader = true
	}

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
	timeout := time.NewTimer(30 * time.Second)
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

			if IsSequenceHeaderTag(tag) {
				_, _ = c.Writer.Write(tag)
				c.Writer.Flush()
				sentSeqHeader = true
				continue
			}

			if !sentSeqHeader {
				if hubSeq := hub.GetSeqHeaderTag(streamKey); len(hubSeq) > 0 {
					_, _ = c.Writer.Write(hubSeq)
					c.Writer.Flush()
					sentSeqHeader = true
				} else {
					continue
				}
			}

			if waitingForKeyframe {
				if !IsKeyframeTag(tag) {
					continue
				}
				waitingForKeyframe = false
			}

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

			if _, err := c.Writer.Write(outTag); err != nil {
				return
			}
			c.Writer.Flush()

		case <-timeout.C:
			return
		}
	}
}

func streamTestPattern(c *gin.Context) {
	seqTag := MakeAVCSequenceHeaderTag(TestSPS, TestPPS, 0)
	if len(seqTag) > 0 {
		_, _ = c.Writer.Write(seqTag)
		c.Writer.Flush()
	}

	ticker := time.NewTicker(40 * time.Millisecond)
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
			isKeyframe := frameIdx%25 == 0
			slice := TestPSlice
			if isKeyframe {
				slice = TestIDRSlice
			}
			flvPayload := FormatNALUsForFLV([][]byte{slice})
			tag := MakeVideoTag(isKeyframe, false, dts, flvPayload)
			if _, err := c.Writer.Write(tag); err != nil {
				return
			}
			c.Writer.Flush()
			frameIdx++
		}
	}
}
