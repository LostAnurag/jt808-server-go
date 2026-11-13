package model

// 数据上行透传 (0x0900)
type Msg0900 struct {
	Header  *MsgHeader `json:"header"`
	Type    byte       `json:"type"`
	Content []byte     `json:"content"`
}

func (m *Msg0900) Decode(packet *PacketData) error {
	m.Header = packet.Header
	if len(packet.Body) > 0 {
		m.Type = packet.Body[0]
		if len(packet.Body) > 1 {
			m.Content = packet.Body[1:]
		}
	}
	return nil
}

func (m *Msg0900) Encode() (pkt []byte, err error) {
	pkt, err = writeHeader(m, pkt)
	if err != nil {
		return nil, err
	}
	pkt = append(pkt, m.Type)
	pkt = append(pkt, m.Content...)
	return pkt, nil
}

func (m *Msg0900) GetHeader() *MsgHeader {
	return m.Header
}

func (m *Msg0900) GenOutgoing(_ JT808Msg) error {
	return nil
}
