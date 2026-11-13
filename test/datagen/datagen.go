package datagen

import (
	"bytes"
	"math"
	"math/rand"
	"strconv"
	"sync"
	"time"

	regen "github.com/AnatolyRugalev/goregen"
	"github.com/rs/zerolog/log"

	"github.com/fakeyanss/jt808-server-go/internal/codec/hex"
	"github.com/fakeyanss/jt808-server-go/internal/config"
	"github.com/fakeyanss/jt808-server-go/internal/protocol/model"
)

func matchTokenAndGen(input string) string {
	output, _ := regen.Generate(input)
	return output
}

func atoi(input string) int64 {
	output, err := strconv.Atoi(input)
	if err != nil {
		return 0
	}
	return int64(output)
}

func atof(input string) float64 {
	output, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0
	}
	return output
}

func GenDevice(deviceConf *config.DeviceConf) *model.Device {
	device := &model.Device{
		ID:              matchTokenAndGen(deviceConf.IDReg),
		Plate:           matchTokenAndGen(deviceConf.PlateReg),
		Phone:           matchTokenAndGen(deviceConf.PhoneReg),
		Keepalive:       time.Duration(deviceConf.Keepalive) * 1000 * 1000 * 1000, // s -> ns
		IMEI:            matchTokenAndGen(deviceConf.IMEIReg),
		SoftwareVersion: "fakeyanss.github.io",
	}
	switch deviceConf.ProtocolVersion {
	case "2019":
		device.VersionDesc = model.Version2019
		device.Phone = fillDevicePhone(device.Phone, 20)
	case "2013":
		device.VersionDesc = model.Version2013
		device.Phone = fillDevicePhone(device.Phone, 12)
	case "2011":
		device.VersionDesc = model.Version2011
		device.Phone = fillDevicePhone(device.Phone, 12)
	}
	log.Debug().Str("device", device.Phone).Msgf("Generate random device=%+v", device)
	return device
}

func fillDevicePhone(phone string, dstLen int) string {
	var b bytes.Buffer
	for i := len(phone); i < dstLen; i++ {
		b.WriteString("0")
	}
	b.WriteString(phone)
	return b.String()
}

func genMsgHeader(msgID uint16, device *model.Device) *model.MsgHeader {
	msgHeader := &model.MsgHeader{
		MsgID: msgID,
		Attr: &model.MsgBodyAttr{
			Encryption:           uint8(model.EncryptionNone),
			PacketFragmented:     0,
			VersionSign:          1,
			Extra:                0,
			EncryptionDesc:       model.EncryptionNone,
			PacketFragmentedDesc: model.PacketFragmentedFalse,
			VersionDesc:          model.Version2019,
		},
		ProtocolVersion: 1,
		PhoneNumber:     device.Phone,
		SerialNumber:    1,
		Frag:            nil,
	}
	if device.VersionDesc == model.Version2019 {
		msgHeader.Attr.VersionSign = 1
		msgHeader.Attr.VersionDesc = model.Version2019
		msgHeader.ProtocolVersion = 1
	} else {
		msgHeader.Attr.VersionSign = 0
		msgHeader.Attr.VersionDesc = model.Version2013
		msgHeader.ProtocolVersion = 0
	}
	return msgHeader
}

func GenMsg0002(device *model.Device) *model.Msg0002 {
	return &model.Msg0002{
		Header: genMsgHeader(model.MsgID0002, device),
	}
}

func GenMsg0100(deviceConf *config.DeviceConf, device *model.Device) *model.Msg0100 {
	return &model.Msg0100{
		Header:         genMsgHeader(model.MsgID0100, device),
		ProvinceID:     uint16(atoi(matchTokenAndGen(deviceConf.ProvinceIDReg))),
		CityID:         uint16(atoi(matchTokenAndGen(deviceConf.CityIDReg))),
		ManufacturerID: "fakeyanss",
		DeviceMode:     "fakeyanss",
		DeviceID:       device.ID,
		PlateColor:     byte(atoi(matchTokenAndGen(deviceConf.PlateColorReg))),
		PlateNumber:    device.Plate,
	}
}

func genGeoMeta(conf *config.DeviceGeoConf) *model.GeoMeta {
	return &model.GeoMeta{
		ACCStatus:              byte(atoi(matchTokenAndGen(conf.Geo.ACCStatusReg))),
		LocationStatus:         byte(atoi(matchTokenAndGen(conf.Geo.LocationStatusReg))),
		LatitudeType:           byte(atoi(matchTokenAndGen(conf.Geo.LatitudeTypeReg))),
		LongitudeType:          byte(atoi(matchTokenAndGen(conf.Geo.LongitudeTypeReg))),
		OperatingStatus:        byte(atoi(matchTokenAndGen(conf.Geo.OperatingStatusReg))),
		GeoEncryptionStatus:    byte(atoi(matchTokenAndGen(conf.Geo.GeoEncryptionStatusReg))),
		LoadStatus:             byte(atoi(matchTokenAndGen(conf.Geo.LoadStatusReg))),
		FuelSystemStatus:       byte(atoi(matchTokenAndGen(conf.Geo.FuelSystemStatusReg))),
		AlternatorSystemStatus: byte(atoi(matchTokenAndGen(conf.Geo.AlternatorSystemStatusReg))),
		DoorLockedStatus:       byte(atoi(matchTokenAndGen(conf.Geo.DoorLockedStatusReg))),
		FrontDoorStatus:        byte(atoi(matchTokenAndGen(conf.Geo.FrontDoorStatusReg))),
		MidDoorStatus:          byte(atoi(matchTokenAndGen(conf.Geo.MidDoorStatusReg))),
		BackDoorStatus:         byte(atoi(matchTokenAndGen(conf.Geo.BackDoorStatusReg))),
		DriverDoorStatus:       byte(atoi(matchTokenAndGen(conf.Geo.DriverDoorStatusReg))),
		CustomDoorStatus:       byte(atoi(matchTokenAndGen(conf.Geo.CustomDoorStatusReg))),
		GPSLocationStatus:      byte(atoi(matchTokenAndGen(conf.Geo.GPSLocationStatusReg))),
		BeidouLocationStatus:   byte(atoi(matchTokenAndGen(conf.Geo.BeidouLocationStatusReg))),
		GLONASSLocationStatus:  byte(atoi(matchTokenAndGen(conf.Geo.GLONASSLocationStatusReg))),
		GalileoLocationStatus:  byte(atoi(matchTokenAndGen(conf.Geo.GalileoLocationStatusReg))),
		DrivingStatus:          byte(atoi(matchTokenAndGen(conf.Geo.DrivingStatusReg))),
	}
}

type Waypoint struct {
	Lat float64
	Lng float64
}

// Indore to Delhi highway route waypoints (via NH 52 -> NH 46 -> NH 44 / Yamuna Expressway)
var IndoreToDelhiRoute = []Waypoint{
	{22.7196, 75.8577}, // Indore (Start)
	{22.7930, 75.9080}, // Indore Bypass / Manglaya
	{22.9676, 76.0534}, // Dewas
	{23.2500, 76.1500}, // Maksi
	{23.4294, 76.2774}, // Shajapur
	{23.5700, 76.4680}, // Sarangpur
	{23.7200, 76.6800}, // Pachore
	{23.9723, 76.9180}, // Biaora
	{24.1800, 77.0100}, // Chachaura
	{24.4400, 77.1900}, // Raghogarh
	{24.6465, 77.3090}, // Guna
	{24.9700, 77.5600}, // Badarwas
	{25.2300, 77.6000}, // Kolaras
	{25.4244, 77.6582}, // Shivpuri
	{25.8800, 77.9200}, // Mohana
	{26.2183, 78.1828}, // Gwalior
	{26.4950, 77.9940}, // Morena
	{26.7025, 77.8934}, // Dholpur
	{27.1767, 78.0081}, // Agra
	{27.4924, 77.6737}, // Mathura
	{27.7900, 77.4300}, // Kosi Kalan
	{27.8900, 77.3700}, // Hodal
	{28.1487, 77.3260}, // Palwal
	{28.4089, 77.3178}, // Faridabad
	{28.6139, 77.2090}, // Delhi (Destination)
}

var (
	routeIndexMutex sync.Mutex
	routeIndexMap   = make(map[string]int) // phone -> target waypoint index
)

func distanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLng := (lng2 - lng1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return 6371.0 * c
}

func bearingDeg(lat1, lng1, lat2, lng2 float64) uint16 {
	phi1 := lat1 * math.Pi / 180.0
	phi2 := lat2 * math.Pi / 180.0
	deltaLambda := (lng2 - lng1) * math.Pi / 180.0

	y := math.Sin(deltaLambda) * math.Cos(phi2)
	x := math.Cos(phi1)*math.Sin(phi2) - math.Sin(phi1)*math.Cos(phi2)*math.Cos(deltaLambda)
	theta := math.Atan2(y, x) * 180.0 / math.Pi
	b := int(math.Round(theta+360)) % 360
	return uint16(b)
}

func GenDeviceGeo(conf *config.DeviceGeoConf, device *model.Device) *model.DeviceGeo {
	lat := atof(matchTokenAndGen(conf.Location.LatitudeReg))
	lng := atof(matchTokenAndGen(conf.Location.LongitudeReg))
	if lat == 0 && lng == 0 {
		lat = IndoreToDelhiRoute[0].Lat
		lng = IndoreToDelhiRoute[0].Lng
	}

	routeIndexMutex.Lock()
	routeIndexMap[device.Phone] = 1 // Start by targeting the next waypoint
	routeIndexMutex.Unlock()

	initBearing := bearingDeg(lat, lng, IndoreToDelhiRoute[1].Lat, IndoreToDelhiRoute[1].Lng)

	deviceGeo := &model.DeviceGeo{
		Phone: device.Phone,
		Geo:   genGeoMeta(conf),
		Location: &model.Location{
			Latitude:  lat,
			Longitude: lng,
			Altitude:  uint16(atoi(matchTokenAndGen(conf.Location.AltitudeReg))),
		},
		Drive: &model.Drive{
			Speed:     float64(atoi(matchTokenAndGen(conf.Drive.SpeedReg))),
			Direction: initBearing,
		},
	}

	return deviceGeo
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func GenMsg0200(conf *config.DeviceGeoConf, device *model.Device, deviceGeo *model.DeviceGeo) *model.Msg0200 {
	speedKmh := deviceGeo.Drive.Speed
	if speedKmh <= 0 {
		speedKmh = 70.0
	}
	intervalSec := float64(conf.LocationReportInterval)
	if intervalSec <= 0 {
		intervalSec = 10.0
	}

	// Distance traveled in this interval (km)
	distStepKm := speedKmh * (intervalSec / 3600.0)

	routeIndexMutex.Lock()
	targetIdx, exists := routeIndexMap[device.Phone]
	if !exists || targetIdx <= 0 || targetIdx >= len(IndoreToDelhiRoute) {
		targetIdx = 1
		routeIndexMap[device.Phone] = targetIdx
	}
	targetWp := IndoreToDelhiRoute[targetIdx]

	currLat := deviceGeo.Location.Latitude
	currLng := deviceGeo.Location.Longitude

	distToTarget := distanceKm(currLat, currLng, targetWp.Lat, targetWp.Lng)

	// If we've reached the current waypoint, switch to the next one
	if distToTarget <= distStepKm || distToTarget < 0.1 {
		currLat = targetWp.Lat
		currLng = targetWp.Lng
		targetIdx = (targetIdx + 1) % len(IndoreToDelhiRoute)
		if targetIdx == 0 {
			targetIdx = 1
			currLat = IndoreToDelhiRoute[0].Lat
			currLng = IndoreToDelhiRoute[0].Lng
		}
		routeIndexMap[device.Phone] = targetIdx
		targetWp = IndoreToDelhiRoute[targetIdx]
		distToTarget = distanceKm(currLat, currLng, targetWp.Lat, targetWp.Lng)
	}

	// Calculate precise highway bearing to target waypoint
	heading := bearingDeg(currLat, currLng, targetWp.Lat, targetWp.Lng)
	deviceGeo.Drive.Direction = heading

	// Advance fraction along current route segment
	if distToTarget > 0 {
		frac := distStepKm / distToTarget
		if frac > 1.0 {
			frac = 1.0
		}
		deviceGeo.Location.Latitude = currLat + frac*(targetWp.Lat-currLat)
		deviceGeo.Location.Longitude = currLng + frac*(targetWp.Lng-currLng)
	}

	routeIndexMutex.Unlock()

	// Natural speed fluctuation (+/- 5 km/h around base speed)
	speedOffset := rand.Float64()*6 - 3
	newSpeed := deviceGeo.Drive.Speed + speedOffset
	if newSpeed < 50 {
		newSpeed = 65
	} else if newSpeed > 95 {
		newSpeed = 75
	}
	deviceGeo.Drive.Speed = newSpeed

	m := &model.Msg0200{
		Header:     genMsgHeader(model.MsgID0200, device),
		AlarmSign:  1024,                      // 暂不支持
		StatusSign: genGeoMeta(conf).Encode(), // 每次生成新的status
		Latitude:   uint32(deviceGeo.Location.Latitude * model.LocationAccuracy),
		Longitude:  uint32(deviceGeo.Location.Longitude * model.LocationAccuracy),
		Altitude:   deviceGeo.Location.Altitude,
		Speed:      uint16(deviceGeo.Drive.Speed * model.SpeedAccuracy),
		Direction:  deviceGeo.Drive.Direction,
	}
	m.Time = hex.FormatTime(time.Now())

	return m
}
