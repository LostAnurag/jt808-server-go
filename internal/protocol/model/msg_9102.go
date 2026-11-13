package model

import (
	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
)

// JT1078 音视频实时传输控制 (0x9102)
// 平台向终端发送控制指令，如停止推流、切换码流、暂停/恢复
type Msg9102 struct {
	Header           *MsgHeader `json:"header"`
	ChannelNumber    uint8      `json:"channelNumber"`    // 逻辑通道号
	ControlCmd       uint8      `json:"controlCmd"`       // 控制指令: 0: 关闭音视频传输, 1: 切换码流, 2: 暂停, 3: 恢复, 4: 关闭双向对讲
	CloseType        uint8      `json:"closeType"`        // 关闭音视频类型: 0: 关闭音视频, 1: 只关音频, 2: 只关视频
	SwitchStreamType uint8      `json:"switchStreamType"` // 切换码流类型: 0: 主码流, 1: 子码流
}

func (m *Msg9102) Decode(packet *PacketData) error {
	m.Header = packet.Header
	pkt, idx := packet.Body, 0
	if len(pkt) < 4 {
		return ErrDecodeMsg
	}

	m.ChannelNumber = hex.ReadByte(pkt, &idx)
	m.ControlCmd = hex.ReadByte(pkt, &idx)
	m.CloseType = hex.ReadByte(pkt, &idx)
	m.SwitchStreamType = hex.ReadByte(pkt, &idx)

	return nil
}

func (m *Msg9102) Encode() (pkt []byte, err error) {
	pkt = hex.WriteByte(pkt, m.ChannelNumber)
	pkt = hex.WriteByte(pkt, m.ControlCmd)
	pkt = hex.WriteByte(pkt, m.CloseType)
	pkt = hex.WriteByte(pkt, m.SwitchStreamType)

	pkt, err = writeHeader(m, pkt)
	return pkt, err
}

func (m *Msg9102) GetHeader() *MsgHeader {
	return m.Header
}

func (m *Msg9102) GenOutgoing(_ JT808Msg) error {
	return nil
}
