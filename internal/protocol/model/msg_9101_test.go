package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMsg9101_EncodeAndDecode(t *testing.T) {
	header := &MsgHeader{
		MsgID:        MsgID9101,
		PhoneNumber:  "377077047531",
		SerialNumber: 42,
		Attr: &MsgBodyAttr{
			VersionDesc: Version2013,
		},
	}

	msg := &Msg9101{
		Header:        header,
		ServerIP:      "192.168.1.100",
		TCPPort:       1078,
		UDPPort:       1078,
		ChannelNumber: 1, // CH1 Front 1080P
		DataType:      0, // Audio & Video
		StreamType:    1, // Sub stream (720P)
	}

	encodedBytes, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotEmpty(t, encodedBytes)

	// Decode header
	decodedHeader := &MsgHeader{}
	err = decodedHeader.Decode(encodedBytes)
	assert.NoError(t, err)
	assert.Equal(t, uint16(MsgID9101), decodedHeader.MsgID)
	assert.Equal(t, "377077047531", decodedHeader.PhoneNumber)

	// Decode body
	packet := &PacketData{
		Header: decodedHeader,
		Body:   encodedBytes[decodedHeader.Idx:],
	}

	decodedMsg := &Msg9101{}
	err = decodedMsg.Decode(packet)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.1.100", decodedMsg.ServerIP)
	assert.Equal(t, uint16(1078), decodedMsg.TCPPort)
	assert.Equal(t, uint16(1078), decodedMsg.UDPPort)
	assert.Equal(t, uint8(1), decodedMsg.ChannelNumber)
	assert.Equal(t, uint8(0), decodedMsg.DataType)
	assert.Equal(t, uint8(1), decodedMsg.StreamType)
}
