package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fakeyanss/jt808-server-go/internal/protocol/model"
	"github.com/fakeyanss/jt808-server-go/pkg/logger"
	"github.com/rs/zerolog/log"
)

// ANSI Color Codes
const (
	ansiReset        = "\033[0m"
	ansiBold         = "\033[1m"
	ansiDim          = "\033[2m"
	ansiRed          = "\033[31m"
	ansiGreen        = "\033[32m"
	ansiYellow       = "\033[33m"
	ansiBlue         = "\033[34m"
	ansiMagenta      = "\033[35m"
	ansiCyan         = "\033[36m"
	ansiWhite        = "\033[37m"
	ansiBrightRed    = "\033[1;31m"
	ansiBrightGreen  = "\033[1;32m"
	ansiBrightYellow = "\033[1;33m"
	ansiBrightBlue   = "\033[1;34m"
	ansiBrightMag    = "\033[1;35m"
	ansiBrightCyan   = "\033[1;36m"
	ansiBrightWhite  = "\033[1;37m"
	ansiBgGreen      = "\033[1;97;42m"
	ansiBgBlue       = "\033[1;97;44m"
	ansiBgYellow     = "\033[1;97;43m"
)

var (
	gpsLogMutex  sync.Mutex
	gpsLogWriter io.Writer = os.Stdout
)

// SetGPSLogWriter configures custom output writer (useful for testing)
func SetGPSLogWriter(w io.Writer) {
	gpsLogMutex.Lock()
	defer gpsLogMutex.Unlock()
	if w == nil {
		gpsLogWriter = os.Stdout
	} else {
		gpsLogWriter = w
	}
}

// GPSPacketJSON represents a comprehensive JSON structure of the GPS packet
type GPSPacketJSON struct {
	Header     *GPSHeaderJSON   `json:"header"`
	Location   *GPSLocationJSON `json:"location"`
	Drive      *GPSDriveJSON    `json:"drive"`
	Status     *GPSStatusJSON   `json:"status"`
	RawMsg0200 *model.Msg0200   `json:"rawMsg0200"`
}

type GPSHeaderJSON struct {
	MsgID       string `json:"msgId"`
	MsgName     string `json:"msgName"`
	PhoneNumber string `json:"phoneNumber"`
	SerialNum   uint16 `json:"serialNumber"`
}

type GPSLocationJSON struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  uint16  `json:"altitude"`
}

type GPSDriveJSON struct {
	Speed     float64 `json:"speed"`     // km/h
	Direction uint16  `json:"direction"` // 0-359
}

type GPSStatusJSON struct {
	ACC            string `json:"acc"`            // "ON" / "OFF"
	Positioning    string `json:"positioning"`    // "Active" / "Inactive"
	GPSPositioning bool   `json:"gpsPositioning"` // true / false
	RawAlarmSign   uint32 `json:"rawAlarmSign"`
	RawStatusSign  uint32 `json:"rawStatusSign"`
	Time           string `json:"time"`
}

// BuildGPSPacketJSON builds the structured JSON representation of a Msg0200 and DeviceGeo
func BuildGPSPacketJSON(in *model.Msg0200, dg *model.DeviceGeo) *GPSPacketJSON {
	if in == nil {
		return &GPSPacketJSON{}
	}

	phone := ""
	var serial uint16
	if in.Header != nil {
		phone = in.Header.PhoneNumber
		serial = in.Header.SerialNumber
	}

	var lat, lng float64
	var alt, dir uint16
	var speed float64
	accStr := "OFF"
	posStr := "Inactive"
	gpsPos := false
	timeStr := in.Time

	if dg != nil {
		if dg.Phone != "" {
			phone = dg.Phone
		}
		if dg.Location != nil {
			lat = dg.Location.Latitude
			lng = dg.Location.Longitude
			alt = dg.Location.Altitude
		}
		if dg.Drive != nil {
			speed = dg.Drive.Speed
			dir = dg.Drive.Direction
		}
		if dg.Geo != nil {
			if dg.Geo.ACCStatus == 1 {
				accStr = "ON"
			}
			if dg.Geo.LocationStatus == 1 {
				posStr = "Active"
			}
			if dg.Geo.GPSLocationStatus == 1 {
				gpsPos = true
			}
		}
		if !dg.Time.IsZero() {
			timeStr = dg.Time.Format("2006-01-02 15:04:05")
		}
	} else {
		lat = float64(in.Latitude) / model.LocationAccuracy
		lng = float64(in.Longitude) / model.LocationAccuracy
		alt = in.Altitude
		speed = float64(in.Speed) / model.SpeedAccuracy
		dir = in.Direction
		if (in.StatusSign & 0b00000000000000000000000000000001) > 0 {
			accStr = "ON"
		}
		if (in.StatusSign & 0b00000000000000000000000000000010) > 0 {
			posStr = "Active"
		}
		if (in.StatusSign & 0b00000000000001000000000000000000) > 0 {
			gpsPos = true
		}
	}

	return &GPSPacketJSON{
		Header: &GPSHeaderJSON{
			MsgID:       "0x0200",
			MsgName:     "LocationInformationReport",
			PhoneNumber: phone,
			SerialNum:   serial,
		},
		Location: &GPSLocationJSON{
			Latitude:  lat,
			Longitude: lng,
			Altitude:  alt,
		},
		Drive: &GPSDriveJSON{
			Speed:     speed,
			Direction: dir,
		},
		Status: &GPSStatusJSON{
			ACC:            accStr,
			Positioning:    posStr,
			GPSPositioning: gpsPos,
			RawAlarmSign:   in.AlarmSign,
			RawStatusSign:  in.StatusSign,
			Time:           timeStr,
		},
		RawMsg0200: in,
	}
}

// HighlightJSON takes a JSON string and returns an ANSI colorized string.
func HighlightJSON(src string) string {
	var buf bytes.Buffer
	inString := false
	isEscaped := false
	isKey := false

	// Helper to check if a string is a property key by looking ahead for ':'
	checkIsKey := func(pos int) bool {
		for j := pos; j < len(src); j++ {
			c := src[j]
			if c == '\\' {
				j++
				continue
			}
			if c == '"' {
				// string ended, now check if next non-whitespace is ':'
				for k := j + 1; k < len(src); k++ {
					if src[k] == ' ' || src[k] == '\t' || src[k] == '\r' || src[k] == '\n' {
						continue
					}
					return src[k] == ':'
				}
				return false
			}
		}
		return false
	}

	for i := 0; i < len(src); i++ {
		c := src[i]

		if inString {
			buf.WriteByte(c)
			if isEscaped {
				isEscaped = false
			} else if c == '\\' {
				isEscaped = true
			} else if c == '"' {
				inString = false
				buf.WriteString(ansiReset)
			}
			continue
		}

		if c == '"' {
			inString = true
			isEscaped = false
			isKey = checkIsKey(i + 1)
			if isKey {
				buf.WriteString(ansiBrightCyan)
			} else {
				buf.WriteString(ansiBrightGreen)
			}
			buf.WriteByte(c)
			continue
		}

		if (c >= '0' && c <= '9') || c == '-' {
			// Number start
			buf.WriteString(ansiBrightYellow)
			buf.WriteByte(c)
			for i+1 < len(src) && ((src[i+1] >= '0' && src[i+1] <= '9') || src[i+1] == '.' || src[i+1] == 'e' || src[i+1] == 'E' || src[i+1] == '+' || src[i+1] == '-') {
				i++
				buf.WriteByte(src[i])
			}
			buf.WriteString(ansiReset)
			continue
		}

		if strings.HasPrefix(src[i:], "true") {
			buf.WriteString(ansiBrightMag + "true" + ansiReset)
			i += 3
			continue
		}
		if strings.HasPrefix(src[i:], "false") {
			buf.WriteString(ansiBrightMag + "false" + ansiReset)
			i += 4
			continue
		}
		if strings.HasPrefix(src[i:], "null") {
			buf.WriteString(ansiBrightRed + "null" + ansiReset)
			i += 3
			continue
		}

		if c == '{' || c == '}' || c == '[' || c == ']' {
			buf.WriteString(ansiBrightWhite)
			buf.WriteByte(c)
			buf.WriteString(ansiReset)
			continue
		}

		if c == ':' || c == ',' {
			buf.WriteString(ansiDim)
			buf.WriteByte(c)
			buf.WriteString(ansiReset)
			continue
		}

		buf.WriteByte(c)
	}

	return buf.String()
}

// FormatJT808Hex formats raw bytes into colored, byte-spaced hex and continuous hex string
func FormatJT808Hex(raw []byte, colored bool) (formattedSpaced string, continuousHex string) {
	if len(raw) == 0 {
		return "N/A", "N/A"
	}

	continuousHex = strings.ToUpper(hex.EncodeToString(raw))
	if !colored {
		var parts []string
		for _, b := range raw {
			parts = append(parts, fmt.Sprintf("%02X", b))
		}
		return strings.Join(parts, " "), continuousHex
	}

	var sb strings.Builder
	for i, b := range raw {
		isDelim := (i == 0 || i == len(raw)-1) && b == 0x7e
		isCheckCode := i == len(raw)-2
		isHeader := i >= 1 && i <= 12 // header is typically 12-16 bytes

		if isDelim {
			sb.WriteString(ansiBrightMag)
		} else if isCheckCode {
			sb.WriteString(ansiBrightRed)
		} else if isHeader {
			sb.WriteString(ansiBrightCyan)
		} else {
			sb.WriteString(ansiBrightYellow)
		}

		sb.WriteString(fmt.Sprintf("%02X", b))
		sb.WriteString(ansiReset)

		if i < len(raw)-1 {
			if (i+1)%16 == 0 {
				sb.WriteString("\n  ")
			} else {
				sb.WriteString(" ")
			}
		}
	}

	return sb.String(), continuousHex
}

// LogGPSPacket prints the highlighted GPS packet to console
func LogGPSPacket(rawJT808 []byte, in *model.Msg0200, dg *model.DeviceGeo, isClientSend bool) {
	if !logger.IsHighlightGPSEnabled() {
		return
	}

	gpsLogMutex.Lock()
	defer gpsLogMutex.Unlock()

	// If raw frame bytes missing, synthesize them via encoder
	if len(rawJT808) == 0 && in != nil {
		pc := NewJT808PacketCodec()
		rawJT808, _ = pc.Encode(in)
	}

	// Build JSON
	packetJSON := BuildGPSPacketJSON(in, dg)
	jsonBytes, err := json.MarshalIndent(packetJSON, "  ", "  ")
	jsonStr := ""
	if err == nil {
		jsonStr = string(jsonBytes)
	} else {
		jsonStr = fmt.Sprintf("{\"error\": \"%v\"}", err)
	}
	highlightedJSON := HighlightJSON(jsonStr)

	// Format JT808 hex
	spacedHex, continuousHex := FormatJT808Hex(rawJT808, true)

	// Direction description
	dirDesc := getDirectionDesc(packetJSON.Drive.Direction)

	// Badge styling
	badge := ansiBgGreen + " [ GPS PACKET RECEIVED (0x0200) ] " + ansiReset
	if isClientSend {
		badge = ansiBgBlue + " [ GPS PACKET SENT (0x0200) ] " + ansiReset
	}

	// Status flags
	accColor := ansiBrightRed
	if packetJSON.Status.ACC == "ON" {
		accColor = ansiBrightGreen
	}
	gpsFixedColor := ansiBrightRed
	if packetJSON.Status.GPSPositioning {
		gpsFixedColor = ansiBrightGreen
	}

	nowStr := time.Now().Format("2006-01-02 15:04:05.000")

	// Also log structured event to zerolog so it appears in the log file & web UI
	log.Info().
		Str("phone", packetJSON.Header.PhoneNumber).
		Uint16("serial", packetJSON.Header.SerialNum).
		Float64("lat", packetJSON.Location.Latitude).
		Float64("lng", packetJSON.Location.Longitude).
		Float64("speed", packetJSON.Drive.Speed).
		Str("acc", packetJSON.Status.ACC).
		Str("posStatus", packetJSON.Status.Positioning).
		Str("rawHex", continuousHex).
		RawJSON("gpsJSON", []byte(jsonStr)).
		Msg("GPS Packet Processed")

	coordNote := ""
	if packetJSON.Location.Latitude == 0 && packetJSON.Location.Longitude == 0 {
		coordNote = fmt.Sprintf(" %s(⚠️ Awaiting Fix)%s", ansiBrightYellow, ansiReset)
	}

	fixDesc := "Active"
	if packetJSON.Status.Positioning == "Inactive" {
		fixDesc = "Searching"
	}

	outWriter := gpsLogWriter
	if outWriter == nil {
		outWriter = os.Stdout
	}

	// Print beautiful boxed visual banner to console
	fmt.Fprintf(outWriter, "\n%s╔══════════════════════════════════════════════════════════════════════════════════════╗%s\n", ansiBrightCyan, ansiReset)
	fmt.Fprintf(outWriter, "%s║%s  %s  %sTime: %s%s%s%s║%s\n",
		ansiBrightCyan, ansiReset, badge, ansiDim, nowStr, ansiReset,
		strings.Repeat(" ", max(0, 48-len(nowStr))),
		ansiBrightCyan, ansiReset)
	fmt.Fprintf(outWriter, "%s╠══════════════════════════════════════════════════════════════════════════════════════╣%s\n", ansiBrightCyan, ansiReset)

	// Summary row 1: Device & Timestamp
	fmt.Fprintf(outWriter, "  %s📱 Phone Number :%s %s%-16s%s  %s🔢 Serial No :%s %s%-8d%s  %s⚡ ACC :%s %s%-4s%s\n",
		ansiBrightWhite, ansiReset, ansiBrightYellow, packetJSON.Header.PhoneNumber, ansiReset,
		ansiBrightWhite, ansiReset, ansiBrightYellow, packetJSON.Header.SerialNum, ansiReset,
		ansiBrightWhite, ansiReset, accColor, packetJSON.Status.ACC, ansiReset)

	// Summary row 2: Coordinates & Fix
	fmt.Fprintf(outWriter, "  %s📍 Coordinates  :%s %s%.6f° N, %.6f° E%s%s  %s🏔️  Altitude  :%s %s%d m%s\n",
		ansiBrightWhite, ansiReset, ansiBrightGreen, packetJSON.Location.Latitude, packetJSON.Location.Longitude, ansiReset, coordNote,
		ansiBrightWhite, ansiReset, ansiBrightGreen, packetJSON.Location.Altitude, ansiReset)

	// Summary row 3: Speed, Direction, GPS Status
	fmt.Fprintf(outWriter, "  %s🏎️  Speed        :%s %s%.1f km/h%s  %s🧭 Direction :%s %s%d° (%s)%s  %s🛰️  Fix :%s %s%s%s\n",
		ansiBrightWhite, ansiReset, ansiBrightYellow, packetJSON.Drive.Speed, ansiReset,
		ansiBrightWhite, ansiReset, ansiBrightYellow, packetJSON.Drive.Direction, dirDesc, ansiReset,
		ansiBrightWhite, ansiReset, gpsFixedColor, fixDesc, ansiReset)

	// Separator: JT808 Raw Packet
	fmt.Fprintf(outWriter, "%s╟──────────────────────────────────────────────────────────────────────────────────────╢%s\n", ansiBrightCyan, ansiReset)
	fmt.Fprintf(outWriter, "  %s📦 JT808 RAW PACKET (%d bytes):%s\n", ansiBrightWhite, len(rawJT808), ansiReset)
	fmt.Fprintf(outWriter, "  %sContinuous Hex:%s %s%s%s\n", ansiDim, ansiReset, ansiCyan, continuousHex, ansiReset)
	fmt.Fprintf(outWriter, "  %sByte-spaced Hex:%s\n  %s\n", ansiDim, ansiReset, spacedHex)

	// Separator: JSON
	fmt.Fprintf(outWriter, "%s╟──────────────────────────────────────────────────────────────────────────────────────╢%s\n", ansiBrightCyan, ansiReset)
	fmt.Fprintf(outWriter, "  %s📄 GPS PACKET (JSON):%s\n", ansiBrightWhite, ansiReset)
	fmt.Fprintf(outWriter, "  %s\n", highlightedJSON)

	// Footer border
	fmt.Fprintf(outWriter, "%s╚══════════════════════════════════════════════════════════════════════════════════════╝%s\n\n", ansiBrightCyan, ansiReset)
}

func getDirectionDesc(dir uint16) string {
	switch {
	case dir == 0 || dir == 360:
		return "North"
	case dir > 0 && dir < 90:
		return "North-East"
	case dir == 90:
		return "East"
	case dir > 90 && dir < 180:
		return "South-East"
	case dir == 180:
		return "South"
	case dir > 180 && dir < 270:
		return "South-West"
	case dir == 270:
		return "West"
	case dir > 270 && dir < 360:
		return "North-West"
	default:
		return "Unknown"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
