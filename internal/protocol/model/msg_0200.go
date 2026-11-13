package model

import (
	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
)

// ADASAlert 高级驾驶辅助系统报警信息 (JSATL 12-2017 苏标 0x64 附加信息)
type ADASAlert struct {
	AlarmID       uint32 `json:"alarmId"`
	FlagStatus    uint8  `json:"flagStatus"`    // 0: 不可用; 1: 开始标志; 2: 结束标志
	AlarmType     uint8  `json:"alarmType"`     // 1: 前向碰撞, 2: 车道偏离, 3: 车距过近, 4: 行人碰撞, 5: 频繁变道, 7: 障碍物
	AlarmTypeName string `json:"alarmTypeName"` // 报警类型名称
	AlarmLevel    uint8  `json:"alarmLevel"`    // 1: 一级报警(提示); 2: 二级报警(严重)
	FrontSpeed    uint8  `json:"frontSpeed"`    // 前车车速 (km/h)
	FrontDistance uint8  `json:"frontDistance"` // 前车/行人距离 (100ms 或 0.1m)
	DeviateType   uint8  `json:"deviateType"`   // 偏离类型: 1: 左偏离; 2: 右偏离
}

// DMSAlert 驾驶员状态监测系统报警信息 (JSATL 12-2017 苏标 0x65 附加信息)
type DMSAlert struct {
	AlarmID       uint32 `json:"alarmId"`
	FlagStatus    uint8  `json:"flagStatus"`    // 0: 不可用; 1: 开始标志; 2: 结束标志
	AlarmType     uint8  `json:"alarmType"`     // 1: 疲劳驾驶, 2: 接打电话, 3: 抽烟, 4: 分神驾驶, 5: 驾驶人异常
	AlarmTypeName string `json:"alarmTypeName"` // 报警类型名称
	AlarmLevel    uint8  `json:"alarmLevel"`    // 1: 一级报警; 2: 二级报警
	FatigueDegree uint8  `json:"fatigueDegree"` // 疲劳程度 (1-10)
}

// ExtraInfo 常见标准附加信息
type ExtraInfo struct {
	Mileage        float64 `json:"mileage"`        // 里程 (km), TLV 0x01
	Oil            float64 `json:"oil"`            // 油量 (L), TLV 0x02
	RecordSpeed    float64 `json:"recordSpeed"`    // 行驶记录仪速度 (km/h), TLV 0x03
	SignalStrength uint8   `json:"signalStrength"` // 无线通信网络信号强度 (CSQ 0-31), TLV 0x30
	Satellites     uint8   `json:"satellites"`     // GNSS定位卫星数, TLV 0x31
}

func GetADASAlarmName(alarmType uint8) string {
	switch alarmType {
	case 0x01:
		return "Forward Collision Warning (FCW)"
	case 0x02:
		return "Lane Departure Warning (LDW)"
	case 0x03:
		return "Safe Distance Warning (HMW)"
	case 0x04:
		return "Pedestrian Collision Warning (PCW)"
	case 0x05:
		return "Frequent Lane Change Warning"
	case 0x06:
		return "Road Sign Overspeed Warning"
	case 0x07:
		return "Obstacle Detection Warning"
	default:
		return "ADAS Warning"
	}
}

func GetDMSAlarmName(alarmType uint8) string {
	switch alarmType {
	case 0x01:
		return "Driver Fatigue Detection"
	case 0x02:
		return "Mobile Phone Usage Detection"
	case 0x03:
		return "Smoking Detection"
	case 0x04:
		return "Distracted Driving Alert"
	case 0x05:
		return "Driver Absent / Abnormal"
	default:
		return "DMS Warning"
	}
}

// 位置信息汇报
type Msg0200 struct {
	Header          *MsgHeader        `json:"header"`
	AlarmSign       uint32            `json:"alarmSign"`  // 报警标志位
	StatusSign      uint32            `json:"statusSign"` // 状态标志位
	Latitude        uint32            `json:"latitude"`   // 纬度，以度为单位的纬度值乘以10的6次方，精确到百万分之一度
	Longitude       uint32            `json:"longitude"`  // 精度，以度为单位的经度值乘以10的6次方，精确到百万分之一度
	Altitude        uint16            `json:"altitude"`   // 高程，海拔高度，单位为米(m)
	Speed           uint16            `json:"speed"`      // 速度，单位为0.1公里每小时(1/10km/h)
	Direction       uint16            `json:"direction"`  // 方向，0-359，正北为 0，顺时针
	Time            string            `json:"time"`       // YY-MM-DD-hh-mm-ss(GMT+8 时间)
	ExtraAttributes map[uint8][]byte  `json:"extraAttributes,omitempty"`
	ADAS            *ADASAlert        `json:"adas,omitempty"`
	DMS             *DMSAlert         `json:"dms,omitempty"`
	Extra           *ExtraInfo        `json:"extra,omitempty"`
}

func (m *Msg0200) Decode(packet *PacketData) error {
	m.Header = packet.Header
	pkt, idx := packet.Body, 0
	if len(pkt) < 28 {
		return ErrDecodeMsg
	}

	m.AlarmSign = hex.ReadDoubleWord(pkt, &idx)
	m.StatusSign = hex.ReadDoubleWord(pkt, &idx)
	m.Latitude = hex.ReadDoubleWord(pkt, &idx)
	m.Longitude = hex.ReadDoubleWord(pkt, &idx)
	m.Altitude = hex.ReadWord(pkt, &idx)
	m.Speed = hex.ReadWord(pkt, &idx)
	m.Direction = hex.ReadWord(pkt, &idx)
	m.Time = hex.ReadBCD(pkt, &idx, 6)

	m.ExtraAttributes = make(map[uint8][]byte)
	m.Extra = &ExtraInfo{}

	// Parse additional information (附加信息 TLVs)
	for idx+2 <= len(pkt) {
		id := hex.ReadByte(pkt, &idx)
		length := int(hex.ReadByte(pkt, &idx))
		if idx+length > len(pkt) {
			break
		}
		val := pkt[idx : idx+length]
		idx += length

		m.ExtraAttributes[id] = val
		m.decodeExtraAttribute(id, val)
	}

	return nil
}

func (m *Msg0200) decodeExtraAttribute(id uint8, val []byte) {
	vIdx := 0
	switch id {
	case 0x01: // 里程，DWORD，1/10km
		if len(val) >= 4 {
			mileageRaw := hex.ReadDoubleWord(val, &vIdx)
			m.Extra.Mileage = float64(mileageRaw) / 10.0
		}
	case 0x02: // 油量，WORD，1/10L
		if len(val) >= 2 {
			oilRaw := hex.ReadWord(val, &vIdx)
			m.Extra.Oil = float64(oilRaw) / 10.0
		}
	case 0x03: // 行驶记录功能速度，WORD，1/10km/h
		if len(val) >= 2 {
			speedRaw := hex.ReadWord(val, &vIdx)
			m.Extra.RecordSpeed = float64(speedRaw) / 10.0
		}
	case 0x30: // 无线通信网络信号强度，BYTE
		if len(val) >= 1 {
			m.Extra.SignalStrength = hex.ReadByte(val, &vIdx)
		}
	case 0x31: // GNSS 定位卫星数，BYTE
		if len(val) >= 1 {
			m.Extra.Satellites = hex.ReadByte(val, &vIdx)
		}
	case 0x64: // ADAS 报警 (苏标)
		if len(val) >= 5 {
			adas := &ADASAlert{
				AlarmID:    hex.ReadDoubleWord(val, &vIdx),
				FlagStatus: hex.ReadByte(val, &vIdx),
			}
			if vIdx < len(val) {
				adas.AlarmType = hex.ReadByte(val, &vIdx)
				adas.AlarmTypeName = GetADASAlarmName(adas.AlarmType)
			}
			if vIdx < len(val) {
				adas.AlarmLevel = hex.ReadByte(val, &vIdx)
			}
			if vIdx < len(val) {
				adas.FrontSpeed = hex.ReadByte(val, &vIdx)
			}
			if vIdx < len(val) {
				adas.FrontDistance = hex.ReadByte(val, &vIdx)
			}
			if vIdx < len(val) {
				adas.DeviateType = hex.ReadByte(val, &vIdx)
			}
			m.ADAS = adas
		}
	case 0x65: // DMS/DSM 报警 (苏标)
		if len(val) >= 5 {
			dms := &DMSAlert{
				AlarmID:    hex.ReadDoubleWord(val, &vIdx),
				FlagStatus: hex.ReadByte(val, &vIdx),
			}
			if vIdx < len(val) {
				dms.AlarmType = hex.ReadByte(val, &vIdx)
				dms.AlarmTypeName = GetDMSAlarmName(dms.AlarmType)
			}
			if vIdx < len(val) {
				dms.AlarmLevel = hex.ReadByte(val, &vIdx)
			}
			if vIdx < len(val) {
				dms.FatigueDegree = hex.ReadByte(val, &vIdx)
			}
			m.DMS = dms
		}
	}
}

func (m *Msg0200) EncodeBody() []byte {
	pkt := make([]byte, 0)
	pkt = hex.WriteDoubleWord(pkt, m.AlarmSign)
	pkt = hex.WriteDoubleWord(pkt, m.StatusSign)
	pkt = hex.WriteDoubleWord(pkt, m.Latitude)
	pkt = hex.WriteDoubleWord(pkt, m.Longitude)
	pkt = hex.WriteWord(pkt, m.Altitude)
	pkt = hex.WriteWord(pkt, m.Speed)
	pkt = hex.WriteWord(pkt, m.Direction)
	pkt = hex.WriteBCD(pkt, m.Time)

	for id, val := range m.ExtraAttributes {
		pkt = hex.WriteByte(pkt, id)
		pkt = hex.WriteByte(pkt, byte(len(val)))
		pkt = append(pkt, val...)
	}

	return pkt
}

func (m *Msg0200) Encode() (pkt []byte, err error) {
	pkt = m.EncodeBody()
	pkt, err = writeHeader(m, pkt)
	return pkt, err
}

func (m *Msg0200) GetHeader() *MsgHeader {
	return m.Header
}

func (m *Msg0200) GenOutgoing(_ JT808Msg) error {
	return nil
}
