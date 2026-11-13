package media

import (
	"encoding/binary"
)

// FLVHeader returns the 9-byte FLV header followed by 4-byte PreviousTagSize0 (total 13 bytes).
// TypeFlags: 0x01 = video only, 0x05 = audio + video.
func FLVHeader(hasAudio bool) []byte {
	flags := byte(0x01) // video only
	if hasAudio {
		flags = 0x05
	}
	hdr := make([]byte, 13)
	hdr[0] = 'F'
	hdr[1] = 'L'
	hdr[2] = 'V'
	hdr[3] = 0x01  // version 1
	hdr[4] = flags // type flags
	binary.BigEndian.PutUint32(hdr[5:9], 9) // header length = 9
	binary.BigEndian.PutUint32(hdr[9:13], 0) // PreviousTagSize0 = 0
	return hdr
}

// MakeAVCSequenceHeaderTag creates an FLV video tag (type 0x09) containing
// the AVCDecoderConfigurationRecord for H.264 (AVC) SPS and PPS.
func MakeAVCSequenceHeaderTag(sps, pps []byte, dts uint32) []byte {
	// Build AVCDecoderConfigurationRecord
	// 1 byte: configurationVersion = 1
	// 1 byte: AVCProfileIndication = sps[1]
	// 1 byte: profile_compatibility = sps[2]
	// 1 byte: AVCLevelIndication = sps[3]
	// 1 byte: 0xFF (lengthSizeMinusOne = 3, meaning 4-byte NALU lengths)
	// 1 byte: 0xE1 (numOfSequenceParameterSets = 1)
	// 2 bytes: spsLength
	// N bytes: sps
	// 1 byte: numOfPictureParameterSets = 1
	// 2 bytes: ppsLength
	// M bytes: pps
	if len(sps) < 4 {
		return nil
	}

	recordLen := 11 + len(sps) + len(pps)
	record := make([]byte, recordLen)
	record[0] = 0x01
	record[1] = sps[1]
	record[2] = sps[2]
	record[3] = sps[3]
	record[4] = 0xFF
	record[5] = 0xE1
	binary.BigEndian.PutUint16(record[6:8], uint16(len(sps)))
	copy(record[8:8+len(sps)], sps)

	offset := 8 + len(sps)
	record[offset] = 0x01
	binary.BigEndian.PutUint16(record[offset+1:offset+3], uint16(len(pps)))
	copy(record[offset+3:], pps)

	return MakeVideoTag(true, true, dts, record)
}

// MakeVideoTag creates a single FLV video tag.
// isKeyframe: true for IDR (0x17), false for non-IDR (0x27).
// isSeqHeader: true if payload is AVCDecoderConfigurationRecord (AVCPacketType=0),
//              false for regular AVC NALU data (AVCPacketType=1).
func MakeVideoTag(isKeyframe, isSeqHeader bool, dts uint32, payload []byte) []byte {
	dataLen := 5 + len(payload)
	tag := make([]byte, 11+dataLen+4)

	// FLV Tag Header (11 bytes)
	tag[0] = 0x09 // TagType: Video
	// DataSize (3 bytes)
	tag[1] = byte((dataLen >> 16) & 0xFF)
	tag[2] = byte((dataLen >> 8) & 0xFF)
	tag[3] = byte(dataLen & 0xFF)
	// Timestamp (lower 24 bits)
	tag[4] = byte((dts >> 16) & 0xFF)
	tag[5] = byte((dts >> 8) & 0xFF)
	tag[6] = byte(dts & 0xFF)
	// TimestampExtended (upper 8 bits)
	tag[7] = byte((dts >> 24) & 0xFF)
	// StreamID (3 bytes = 0)
	tag[8] = 0
	tag[9] = 0
	tag[10] = 0

	// Video Data Header (5 bytes)
	if isKeyframe {
		tag[11] = 0x17 // 1 = keyframe, 7 = AVC
	} else {
		tag[11] = 0x27 // 2 = inter frame, 7 = AVC
	}

	if isSeqHeader {
		tag[12] = 0x00 // AVC sequence header
	} else {
		tag[12] = 0x01 // AVC NALU
	}

	// CompositionTime (3 bytes = 0)
	tag[13] = 0
	tag[14] = 0
	tag[15] = 0

	// Video Payload
	copy(tag[16:16+len(payload)], payload)

	// PreviousTagSize (4 bytes BigEndian) = 11 + DataSize
	totalTagSize := uint32(11 + dataLen)
	binary.BigEndian.PutUint32(tag[16+len(payload):], totalTagSize)

	return tag
}

// FormatNALUsForFLV converts raw NAL units into the AVCVIDEOPACKET format:
// [4-byte NALU length BigEndian] + [NALU bytes] for each unit.
func FormatNALUsForFLV(nalus [][]byte) []byte {
	totalLen := 0
	for _, n := range nalus {
		if len(n) > 0 {
			totalLen += 4 + len(n)
		}
	}
	buf := make([]byte, totalLen)
	idx := 0
	for _, n := range nalus {
		if len(n) == 0 {
			continue
		}
		binary.BigEndian.PutUint32(buf[idx:idx+4], uint32(len(n)))
		idx += 4
		copy(buf[idx:idx+len(n)], n)
		idx += len(n)
	}
	return buf
}

// SetTagDTS modifies the DTS timestamp (bytes 4..7) of an FLV tag.
func SetTagDTS(tag []byte, dts uint32) {
	if len(tag) < 11 {
		return
	}
	tag[4] = byte((dts >> 16) & 0xFF)
	tag[5] = byte((dts >> 8) & 0xFF)
	tag[6] = byte(dts & 0xFF)
	tag[7] = byte((dts >> 24) & 0xFF)
}

// GetTagDTS extracts the DTS timestamp from an FLV tag.
func GetTagDTS(tag []byte) uint32 {
	if len(tag) < 11 {
		return 0
	}
	return uint32(tag[7])<<24 | uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6])
}

// IsKeyframeTag returns true if the FLV tag is an AVC keyframe (0x17).
func IsKeyframeTag(tag []byte) bool {
	if len(tag) <= 11 {
		return false
	}
	return (tag[11] >> 4) == 1
}

// IsSequenceHeaderTag returns true if the FLV tag is an AVC sequence header (AVCPacketType == 0).
func IsSequenceHeaderTag(tag []byte) bool {
	if len(tag) <= 12 {
		return false
	}
	return tag[12] == 0x00
}

