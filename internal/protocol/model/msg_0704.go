package model

import (
	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
)

// 定位数据批量上传 (JT/T 808-2011/2013/2019)
type Msg0704 struct {
	Header *MsgHeader  `json:"header"`
	Count  uint16      `json:"count"` // 数据项个数
	Type   uint8       `json:"type"`  // 位置数据类型: 0: 正常位置批量汇报; 1: 盲区补报
	Items  []*Msg0200 `json:"items"` // 位置汇报数据项
}

func (m *Msg0704) Decode(packet *PacketData) error {
	m.Header = packet.Header
	pkt, idx := packet.Body, 0
	if len(pkt) < 3 {
		return ErrDecodeMsg
	}

	m.Count = hex.ReadWord(pkt, &idx)
	m.Type = hex.ReadByte(pkt, &idx)
	m.Items = make([]*Msg0200, 0, m.Count)

	for i := 0; i < int(m.Count) && idx+2 <= len(pkt); i++ {
		itemLen := int(hex.ReadWord(pkt, &idx))
		if idx+itemLen > len(pkt) {
			break
		}
		itemBytes := pkt[idx : idx+itemLen]
		idx += itemLen

		// Build child header inheriting phone number and version
		childHeader := *m.Header
		childHeader.MsgID = MsgID0200
		childHeader.Attr = &MsgBodyAttr{
			BodyLength:  uint16(itemLen),
			VersionDesc: m.Header.Attr.VersionDesc,
		}

		itemPkt := &PacketData{
			Header: &childHeader,
			Body:   itemBytes,
		}

		item := &Msg0200{}
		if err := item.Decode(itemPkt); err != nil {
			return err
		}
		m.Items = append(m.Items, item)
	}

	return nil
}

func (m *Msg0704) Encode() (pkt []byte, err error) {
	m.Count = uint16(len(m.Items))
	pkt = hex.WriteWord(pkt, m.Count)
	pkt = hex.WriteByte(pkt, m.Type)

	for _, item := range m.Items {
		itemBody := item.EncodeBody()
		pkt = hex.WriteWord(pkt, uint16(len(itemBody)))
		pkt = append(pkt, itemBody...)
	}

	pkt, err = writeHeader(m, pkt)
	return pkt, err
}

func (m *Msg0704) GetHeader() *MsgHeader {
	return m.Header
}

func (m *Msg0704) GenOutgoing(_ JT808Msg) error {
	return nil
}
