package media

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestFLVHeader(t *testing.T) {
	hdr := FLVHeader(false)
	assert.Equal(t, 13, len(hdr))
	assert.Equal(t, byte('F'), hdr[0])
	assert.Equal(t, byte('L'), hdr[1])
	assert.Equal(t, byte('V'), hdr[2])
	assert.Equal(t, byte(0x01), hdr[3])
	assert.Equal(t, byte(0x01), hdr[4]) // video only
}

func TestAVCSequenceHeaderTag(t *testing.T) {
	tag := MakeAVCSequenceHeaderTag(TestSPS, TestPPS, 0)
	assert.NotEmpty(t, tag)
	assert.Equal(t, byte(0x09), tag[0])  // Video tag
	assert.Equal(t, byte(0x17), tag[11]) // Keyframe AVC
	assert.Equal(t, byte(0x00), tag[12]) // AVC sequence header
}

func TestMakeVideoTag(t *testing.T) {
	payload := []byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x01, 0x02, 0x03, 0x04}
	tag := MakeVideoTag(true, false, 1000, payload)
	assert.NotEmpty(t, tag)
	assert.Equal(t, byte(0x09), tag[0])
	assert.Equal(t, byte(0x17), tag[11]) // Keyframe AVC
	assert.Equal(t, byte(0x01), tag[12]) // AVC NALU
}

func TestExtractNALUs(t *testing.T) {
	raw := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xC0, 0x0C,
		0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84,
	}

	nalus := ExtractNALUs(raw)
	assert.Equal(t, 3, len(nalus))
	assert.Equal(t, byte(NALUTypeSPS), GetNALUType(nalus[0]))
	assert.Equal(t, byte(NALUTypePPS), GetNALUType(nalus[1]))
	assert.Equal(t, byte(NALUTypeIDR), GetNALUType(nalus[2]))
}

func TestFormatNALUsForFLV(t *testing.T) {
	nalus := [][]byte{
		{0x67, 0x42},
		{0x68, 0xCE},
	}
	formatted := FormatNALUsForFLV(nalus)
	assert.Equal(t, 4+2+4+2, len(formatted))
	// Check first length = 2
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x02, 0x67, 0x42}, formatted[:6])
}

func TestMediaHubSubscribeAndBroadcast(t *testing.T) {
	hub := GetMediaHub()
	streamKey := StreamKey("377077047531", 1)

	ch, _, _, unsub := hub.Subscribe(streamKey)
	defer unsub()

	testTag := []byte{0x09, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x17, 0x01, 0x00, 0x00, 0x00}
	hub.Broadcast(streamKey, testTag, true, TestSPS, TestPPS)

	// First received item should be the newly generated sequence header
	select {
	case seqReceived := <-ch:
		assert.True(t, IsSequenceHeaderTag(seqReceived))
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for sequence header tag")
	}

	// Second received item should be the video tag itself
	select {
	case received := <-ch:
		assert.True(t, bytes.Equal(testTag, received))
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for broadcast tag")
	}

	assert.True(t, hub.HasActiveStream(streamKey))
}

func TestHandleHTTPFLV(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/live/:stream", HandleHTTPFLV)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/live/test.flv?test=true", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	router.ServeHTTP(w, req)

	res := w.Body.Bytes()
	// Should begin with FLV header
	assert.True(t, len(res) >= 13)
	assert.Equal(t, []byte{'F', 'L', 'V', 0x01, 0x01}, res[:5])
}
