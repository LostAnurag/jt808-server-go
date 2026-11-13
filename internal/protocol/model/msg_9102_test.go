package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMsg9102_EncodeAndDecode(t *testing.T) {
	header := &MsgHeader{
		MsgID:        MsgID9102,
		PhoneNumber:  "377077047531",
		SerialNumber: 43,
		Attr: &MsgBodyAttr{
			VersionDesc: Version2013,
		},
	}

	msg := &Msg9102{
		Header:           header,
		ChannelNumber:    2, // CH2 In-Cabin DMS
		ControlCmd:       0, // Close stream
		CloseType:        0, // Close AV
		SwitchStreamType: 0,
	}

	encodedBytes, err := msg.Encode()
	assert.NoError(t, err)
	assert.NotEmpty(t, encodedBytes)

	// Decode header
	decodedHeader := &MsgHeader{}
	err = decodedHeader.Decode(encodedBytes)
	assert.NoError(t, err)
	assert.Equal(t, uint16(MsgID9102), decodedHeader.MsgID)

	// Decode body
	packet := &PacketData{
		Header: decodedHeader,
		Body:   encodedBytes[decodedHeader.Idx:],
	}

	decodedMsg := &Msg9102{}
	err = decodedMsg.Decode(packet)
	assert.NoError(t, err)
	assert.Equal(t, uint8(2), decodedMsg.ChannelNumber)
	assert.Equal(t, uint8(0), decodedMsg.ControlCmd)
	assert.Equal(t, uint8(0), decodedMsg.CloseType)
}
