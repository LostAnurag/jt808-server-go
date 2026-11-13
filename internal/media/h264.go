package media

import (
	"bytes"
)

// H.264 NAL Unit Types
const (
	NALUTypeNonIDR = 1
	NALUTypeIDR    = 5
	NALUTypeSEI    = 6
	NALUTypeSPS    = 7
	NALUTypePPS    = 8
	NALUTypeAUD    = 9
)

// GetNALUType returns the NAL unit type from the first byte of a NALU.
func GetNALUType(nalu []byte) byte {
	if len(nalu) == 0 {
		return 0
	}
	return nalu[0] & 0x1F
}

// ExtractNALUs parses a byte buffer containing H.264 data (raw Annex B or MPEG-2 PS)
// and returns all identified NAL units (without start codes).
func ExtractNALUs(data []byte) [][]byte {
	var nalus [][]byte
	if len(data) < 4 {
		return nalus
	}

	// Check if data is MPEG-2 PS (starts with 0x000001BA pack header)
	// If so, we extract the video PES packets (stream_id = 0xE0..0xEF)
	cleanData := data
	if bytes.HasPrefix(data, []byte{0x00, 0x00, 0x01, 0xBA}) {
		cleanData = extractPESPayloads(data)
	}

	// Standard Annex B start code scan: 00 00 01 or 00 00 00 01
	n := len(cleanData)
	startIndices := make([]int, 0, 16)

	for i := 0; i <= n-3; i++ {
		if cleanData[i] == 0 && cleanData[i+1] == 0 {
			if cleanData[i+2] == 1 {
				startIndices = append(startIndices, i+3)
				i += 2
			} else if i <= n-4 && cleanData[i+2] == 0 && cleanData[i+3] == 1 {
				startIndices = append(startIndices, i+4)
				i += 3
			}
		}
	}

	for i := 0; i < len(startIndices); i++ {
		start := startIndices[i]
		var end int
		if i+1 < len(startIndices) {
			end = startIndices[i+1]
			// Trim backwards over 00 bytes of the next start code
			for end > start && cleanData[end-1] == 0 {
				end--
			}
			if end > start && cleanData[end-1] == 1 {
				end--
			}
			for end > start && cleanData[end-1] == 0 {
				end--
			}
		} else {
			end = n
			// Trim backwards over trailing 00 padding bytes
			for end > start && cleanData[end-1] == 0 {
				end--
			}
		}

		if end > start {
			nalu := cleanData[start:end]
			nalus = append(nalus, nalu)
		}
	}

	return nalus
}

// extractPESPayloads strips MPEG-2 PS pack headers, system headers, and PES headers,
// extracting pure video payload bytes.
func extractPESPayloads(data []byte) []byte {
	var out bytes.Buffer
	n := len(data)
	i := 0

	for i <= n-4 {
		// Look for start code 00 00 01 xx
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			streamID := data[i+3]
			i += 4

			switch streamID {
			case 0xBA: // Pack header (typically 14 bytes in MPEG-2 PS)
				if i+10 <= n {
					packStuffingLen := int(data[i+9] & 0x07)
					i += 10 + packStuffingLen
				} else {
					i = n
				}
			case 0xBB: // System header
				if i+2 <= n {
					hdrLen := int(data[i])<<8 | int(data[i+1])
					i += 2 + hdrLen
				}
			case 0xBC: // Program Stream Map
				if i+2 <= n {
					psmLen := int(data[i])<<8 | int(data[i+1])
					i += 2 + psmLen
				}
			default:
				// Video PES streams are typically 0xE0..0xEF
				if streamID >= 0xE0 && streamID <= 0xEF {
					if i+2 <= n {
						pesLen := int(data[i])<<8 | int(data[i+1])
						i += 2
						if i+3 <= n {
							pesHdrDataLen := int(data[i+2])
							i += 3 + pesHdrDataLen // skip PES header flags + data
							pesPayloadLen := pesLen - 3 - pesHdrDataLen
							if pesPayloadLen > 0 && i+pesPayloadLen <= n {
								out.Write(data[i : i+pesPayloadLen])
								i += pesPayloadLen
							} else if pesLen == 0 && i < n {
								// Unbounded PES length
								out.Write(data[i:])
								i = n
							}
						}
					}
				}
			}
		} else {
			i++
		}
	}

	if out.Len() == 0 {
		return data // fallback to raw
	}
	return out.Bytes()
}

// Synthetic / Test H.264 Stream
// A valid, minimal H.264 Baseline frame sequence (128x96) compatible with all browser decoders.
// SPS: 67 42 C0 0C DA 02 80 F6 20
// PPS: 68 CE 38 80
var TestSPS = []byte{0x67, 0x42, 0xC0, 0x0C, 0xDA, 0x02, 0x80, 0xF6, 0x20}
var TestPPS = []byte{0x68, 0xCE, 0x38, 0x80}

// Minimal 128x96 IDR keyframe slice
var TestIDRSlice = []byte{
	0x65, 0x88, 0x84, 0x00, 0x33, 0xFF, 0x80, 0x00, 0x0B, 0xFE, 0x40, 0x00, 0x00,
	0x03, 0x00, 0x40, 0x00, 0x00, 0x0F, 0x03, 0xC5, 0x8B, 0xC0, 0x00, 0x00, 0x03,
	0x00, 0x00, 0x03, 0x00, 0x7E, 0x30, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00,
	0xFA, 0x40, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x01, 0xF4, 0x80,
}

// Minimal 128x96 P-frame slice
var TestPSlice = []byte{
	0x41, 0x9A, 0x24, 0x00, 0x33, 0xFF, 0x80, 0x00, 0x0B, 0xFE, 0x40, 0x00, 0x00,
	0x03, 0x00, 0x40, 0x00, 0x00, 0x0F, 0x03, 0xC5, 0x8B, 0xC0,
}
