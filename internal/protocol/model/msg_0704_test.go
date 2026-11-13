package model

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMsg0704_DecodeUserPayload(t *testing.T) {
	// Raw payload from user's actual log:
	// 7e 0704 0063 377077047531 0064 0001 01 005e 00000000 000c0001 01595809 06ca6508 0000 0000 0000 261020002019 ... 2f 7e
	rawHex := "7e070400633770770475310064000101005e00000000000c00010159580906ca6508000000000000261020002019010400000000030200001404000000041504000000001604000000001702000118030000001904000000002504000000002a020000300100310100520100ad02007a2f7e"
	rawBytes, err := hex.DecodeString(rawHex)
	assert.NoError(t, err)

	// Strip 7e delimiter and checksum for packet data
	// [1 : len-2] is header + body, [len-2] is checksum
	pktContent := rawBytes[1 : len(rawBytes)-2]

	header := &MsgHeader{}
	err = header.Decode(pktContent)
	assert.NoError(t, err)
	assert.Equal(t, uint16(0x0704), header.MsgID)
	assert.Equal(t, "377077047531", header.PhoneNumber)
	assert.Equal(t, uint16(0x0064), header.SerialNumber)

	bodyBytes := pktContent[header.Idx:]
	packetData := &PacketData{
		Header: header,
		Body:   bodyBytes,
	}

	msg0704 := &Msg0704{}
	err = msg0704.Decode(packetData)
	assert.NoError(t, err)
	assert.Equal(t, uint16(1), msg0704.Count)
	assert.Equal(t, uint8(1), msg0704.Type) // 盲区补报
	assert.Equal(t, 1, len(msg0704.Items))

	item := msg0704.Items[0]
	assert.Equal(t, uint32(22632457), item.Latitude)
	assert.Equal(t, uint32(113927432), item.Longitude)
	assert.Equal(t, "261020002019", item.Time)
	assert.NotNil(t, item.Extra)

	// Verify DeviceGeo decoding
	dg := &DeviceGeo{}
	err = dg.Decode(header.PhoneNumber, item)
	assert.NoError(t, err)
	assert.InDelta(t, 22.632457, dg.Location.Latitude, 0.000001)
	assert.InDelta(t, 113.927432, dg.Location.Longitude, 0.000001)
	assert.NotNil(t, dg.Alarms)
	assert.False(t, dg.Alarms.SOSAlarm)
}

func TestMsg0200_ADASAndDMSAlarms(t *testing.T) {
	// Construct a Msg0200 with 0x64 (ADAS Forward Collision) and 0x65 (DMS Fatigue)
	header := &MsgHeader{
		MsgID:        MsgID0200,
		PhoneNumber:  "13800138000",
		SerialNumber: 1,
		Attr: &MsgBodyAttr{
			VersionDesc: Version2013,
		},
	}

	m := &Msg0200{
		Header:     header,
		AlarmSign:  0x01, // SOS Alarm
		StatusSign: 0x03, // ACC ON, Positioning Fixed
		Latitude:   31230000,
		Longitude:  121470000,
		Altitude:   10,
		Speed:      600, // 60 km/h
		Direction:  90,
		Time:       "261024103000",
		ExtraAttributes: map[uint8][]byte{
			// 0x01: Mileage 150.5 km (1505 in 1/10 km = 0x000005E1)
			0x01: {0x00, 0x00, 0x05, 0xE1},
			// 0x30: Signal 28
			0x30: {28},
			// 0x31: Satellites 14
			0x31: {14},
			// 0x64: ADAS - Forward Collision Warning (AlarmType 0x01, AlarmLevel 2)
			0x64: {0x00, 0x00, 0x00, 0x01, 0x01, 0x01, 0x02, 50, 15, 0},
			// 0x65: DMS - Driver Fatigue Detection (AlarmType 0x01, AlarmLevel 2, Fatigue 8)
			0x65: {0x00, 0x00, 0x00, 0x02, 0x01, 0x01, 0x02, 8},
		},
	}

	body := m.EncodeBody()
	packet := &PacketData{
		Header: header,
		Body:   body,
	}

	decoded := &Msg0200{}
	err := decoded.Decode(packet)
	assert.NoError(t, err)

	assert.Equal(t, uint32(0x01), decoded.AlarmSign)
	assert.NotNil(t, decoded.Extra)
	assert.InDelta(t, 150.5, decoded.Extra.Mileage, 0.01)
	assert.Equal(t, uint8(28), decoded.Extra.SignalStrength)
	assert.Equal(t, uint8(14), decoded.Extra.Satellites)

	// ADAS verification
	assert.NotNil(t, decoded.ADAS)
	assert.Equal(t, uint8(1), decoded.ADAS.AlarmType)
	assert.Equal(t, "Forward Collision Warning (FCW)", decoded.ADAS.AlarmTypeName)
	assert.Equal(t, uint8(2), decoded.ADAS.AlarmLevel)

	// DMS verification
	assert.NotNil(t, decoded.DMS)
	assert.Equal(t, uint8(1), decoded.DMS.AlarmType)
	assert.Equal(t, "Driver Fatigue Detection", decoded.DMS.AlarmTypeName)
	assert.Equal(t, uint8(8), decoded.DMS.FatigueDegree)

	// DeviceGeo & ActiveAlarms verification
	dg := &DeviceGeo{}
	err = dg.Decode("13800138000", decoded)
	assert.NoError(t, err)
	assert.True(t, dg.Alarms.SOSAlarm)
	assert.Contains(t, dg.ActiveAlarms, "SOS Emergency Alarm")
	assert.Contains(t, dg.ActiveAlarms, "ADAS: Forward Collision Warning (FCW)")
	assert.Contains(t, dg.ActiveAlarms, "DMS: Driver Fatigue Detection")
}
