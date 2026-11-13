package protocol

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
	"github.com/fakeyanss/jt808-server-go/internal/protocol/model"
	"github.com/fakeyanss/jt808-server-go/pkg/logger"
)

func sampleMsg0200() *model.Msg0200 {
	return &model.Msg0200{
		Header: &model.MsgHeader{
			MsgID:        0x0200,
			PhoneNumber:  "013912345678",
			SerialNumber: 42,
		},
		AlarmSign:  0,
		StatusSign: 0b00000000000001000000000000000011, // ACC ON, Positioning Active, GPS Fixed
		Latitude:   39916527,                          // 39.916527
		Longitude:  116397128,                         // 116.397128
		Altitude:   120,
		Speed:      600, // 60.0 km/h
		Direction:  90,  // East
		Time:       "261020103015",
	}
}

func sampleDeviceGeo(m *model.Msg0200) *model.DeviceGeo {
	dg := &model.DeviceGeo{}
	_ = dg.Decode("013912345678", m)
	dg.Time = time.Date(2026, 10, 20, 10, 30, 15, 0, time.UTC)
	return dg
}

func TestBuildGPSPacketJSON(t *testing.T) {
	m := sampleMsg0200()
	dg := sampleDeviceGeo(m)

	gpsJSON := BuildGPSPacketJSON(m, dg)
	require.NotNil(t, gpsJSON)

	assert.Equal(t, "0x0200", gpsJSON.Header.MsgID)
	assert.Equal(t, "LocationInformationReport", gpsJSON.Header.MsgName)
	assert.Equal(t, "013912345678", gpsJSON.Header.PhoneNumber)
	assert.Equal(t, uint16(42), gpsJSON.Header.SerialNum)

	assert.InDelta(t, 39.916527, gpsJSON.Location.Latitude, 0.0001)
	assert.InDelta(t, 116.397128, gpsJSON.Location.Longitude, 0.0001)
	assert.Equal(t, uint16(120), gpsJSON.Location.Altitude)

	assert.InDelta(t, 60.0, gpsJSON.Drive.Speed, 0.1)
	assert.Equal(t, uint16(90), gpsJSON.Drive.Direction)

	assert.Equal(t, "ON", gpsJSON.Status.ACC)
	assert.Equal(t, "Active", gpsJSON.Status.Positioning)
	assert.True(t, gpsJSON.Status.GPSPositioning)
}

func TestHighlightJSON(t *testing.T) {
	jsonSample := `{"phoneNumber": "013912345678", "speed": 60.5, "gpsFixed": true, "extra": null}`
	highlighted := HighlightJSON(jsonSample)

	// Verify ANSI escapes are injected
	assert.Contains(t, highlighted, ansiBrightCyan)   // Key highlight
	assert.Contains(t, highlighted, ansiBrightGreen)  // String value highlight
	assert.Contains(t, highlighted, ansiBrightYellow) // Number highlight
	assert.Contains(t, highlighted, ansiBrightMag)    // Boolean highlight
	assert.Contains(t, highlighted, ansiBrightRed)    // Null highlight
	assert.Contains(t, highlighted, ansiReset)
}

func TestFormatJT808Hex(t *testing.T) {
	raw := hex.Str2Byte("7E0200001C2234567890150000000000000002080301CD779E0728C032003C0000008F230125145158FB7E")

	spaced, continuous := FormatJT808Hex(raw, true)
	assert.Equal(t, "7E0200001C2234567890150000000000000002080301CD779E0728C032003C0000008F230125145158FB7E", continuous)
	assert.Contains(t, spaced, "7E")
	assert.Contains(t, spaced, ansiBrightMag) // 7E delimiter color
	assert.Contains(t, spaced, ansiBrightRed) // FB check code color
}

func TestLogGPSPacket(t *testing.T) {
	m := sampleMsg0200()
	dg := sampleDeviceGeo(m)
	raw := hex.Str2Byte("7E0200001C2234567890150000000000000002080301CD779E0728C032003C0000008F230125145158FB7E")

	var buf bytes.Buffer
	SetGPSLogWriter(&buf)
	defer SetGPSLogWriter(nil)

	logger.SetHighlightGPS(true)
	LogGPSPacket(raw, m, dg, false)

	output := buf.String()

	// Verify key components exist in console log output
	assert.Contains(t, output, "GPS PACKET RECEIVED (0x0200)")
	assert.Contains(t, output, "013912345678")
	assert.Contains(t, output, "39.916527")
	assert.Contains(t, output, "116.397128")
	assert.Contains(t, output, "60.0 km/h")
	assert.Contains(t, output, "JT808 RAW PACKET")
	assert.Contains(t, output, "Continuous Hex:")
	assert.Contains(t, output, "7E0200001C2234567890150000000000000002080301CD779E0728C032003C0000008F230125145158FB7E")
	assert.Contains(t, output, "GPS PACKET (JSON):")
	assert.Contains(t, output, `"msgId"`)
	assert.Contains(t, output, `"0x0200"`)
	assert.Contains(t, output, `"latitude"`)
	assert.Contains(t, output, "39.916527")
	assert.Contains(t, output, `"longitude"`)
	assert.Contains(t, output, "116.397128")

	// Test outgoing client mode
	buf.Reset()
	LogGPSPacket(raw, m, dg, true)
	clientOutput := buf.String()
	assert.Contains(t, clientOutput, "GPS PACKET SENT (0x0200)")

	// Test disabled state
	buf.Reset()
	logger.SetHighlightGPS(false)
	LogGPSPacket(raw, m, dg, false)
	assert.Empty(t, buf.String())
	logger.SetHighlightGPS(true)
}

func TestGetDirectionDesc(t *testing.T) {
	tests := []struct {
		dir  uint16
		want string
	}{
		{0, "North"},
		{45, "North-East"},
		{90, "East"},
		{135, "South-East"},
		{180, "South"},
		{225, "South-West"},
		{270, "West"},
		{315, "North-West"},
		{360, "North"},
		{361, "Unknown"},
	}
	for _, tt := range tests {
		got := getDirectionDesc(tt.dir)
		assert.Equal(t, tt.want, got, "dir %d", tt.dir)
	}
}

func TestVisualGPSPacketOutput(t *testing.T) {
	m := sampleMsg0200()
	dg := sampleDeviceGeo(m)
	raw := hex.Str2Byte("7E0200001C2234567890150000000000000002080301CD779E0728C032003C0000008F230125145158FB7E")

	// Print live to stdout
	LogGPSPacket(raw, m, dg, false)
}
