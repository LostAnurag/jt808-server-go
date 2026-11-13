package model

import (
	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
)

// JT1078 实时音视频传输请求 (0x9101)
// 平台向终端请求实时音视频流
type Msg9101 struct {
	Header        *MsgHeader `json:"header"`
	ServerIP      string     `json:"serverIp"`      // 服务器视频通道监听IP地址
	TCPPort       uint16     `json:"tcpPort"`       // 服务器视频通道监听端口号(TCP)
	UDPPort       uint16     `json:"udpPort"`       // 服务器视频通道监听端口号(UDP)
	ChannelNumber uint8      `json:"channelNumber"` // 逻辑通道号: 1: 前置ADAS摄像头, 2: 车内DMS摄像头, 3: 后置, 4: 扩展
	DataType      uint8      `json:"dataType"`      // 数据类型: 0: 音视频, 1: 视频, 2: 双向对讲, 3: 监听, 4: 中心广播, 5: 透传
	StreamType    uint8      `json:"streamType"`    // 码流类型: 0: 主码流(1080P), 1: 子码流(720P)
}

func (m *Msg9101) Decode(packet *PacketData) error {
	m.Header = packet.Header
	pkt, idx := packet.Body, 0
	if len(pkt) < 8 {
		return ErrDecodeMsg
	}

	ipLen := int(hex.ReadByte(pkt, &idx))
	if idx+ipLen > len(pkt) {
		return ErrDecodeMsg
	}
	m.ServerIP = string(pkt[idx : idx+ipLen])
	idx += ipLen

	if idx+6 > len(pkt) {
		return ErrDecodeMsg
	}
	m.TCPPort = hex.ReadWord(pkt, &idx)
	m.UDPPort = hex.ReadWord(pkt, &idx)
	m.ChannelNumber = hex.ReadByte(pkt, &idx)
	m.DataType = hex.ReadByte(pkt, &idx)
	m.StreamType = hex.ReadByte(pkt, &idx)

	return nil
}

func (m *Msg9101) Encode() (pkt []byte, err error) {
	ipBytes := []byte(m.ServerIP)
	pkt = hex.WriteByte(pkt, byte(len(ipBytes)))
	pkt = append(pkt, ipBytes...)
	pkt = hex.WriteWord(pkt, m.TCPPort)
	pkt = hex.WriteWord(pkt, m.UDPPort)
	pkt = hex.WriteByte(pkt, m.ChannelNumber)
	pkt = hex.WriteByte(pkt, m.DataType)
	pkt = hex.WriteByte(pkt, m.StreamType)

	pkt, err = writeHeader(m, pkt)
	return pkt, err
}

func (m *Msg9101) GetHeader() *MsgHeader {
	return m.Header
}

func (m *Msg9101) GenOutgoing(_ JT808Msg) error {
	return nil
}
