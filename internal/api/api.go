package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mitchellh/mapstructure"
	"github.com/rs/zerolog/log"

	"github.com/fakeyanss/jt808-server-go/internal/config"
	"github.com/fakeyanss/jt808-server-go/internal/media"
	"github.com/fakeyanss/jt808-server-go/internal/protocol/model"
	"github.com/fakeyanss/jt808-server-go/internal/server"
	"github.com/fakeyanss/jt808-server-go/internal/storage"
)

func getDeviceActiveSession(device *model.Device, phone string) (*model.Session, error) {
	if device != nil && device.SessionID != "" {
		if s, err := storage.GetSession(device.SessionID); err == nil && s != nil {
			return s, nil
		}
	}
	if s, err := storage.GetSessionByPhone(phone); err == nil && s != nil {
		if device != nil {
			device.SessionID = s.ID
			device.Conn = s.Conn
		}
		return s, nil
	}
	return nil, storage.ErrSessionClosed
}

func Run(serv *server.TCPServer, cfg *config.Config) {
	// web server structure
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Next()
	})
	cache := storage.GetDeviceCache()
	geoCache := storage.GetGeoCache()

	// Live HTTP-FLV video stream route (from built-in JT1078 media gateway)
	router.GET("/live/:stream", media.HandleHTTPFLV)

	router.GET("/device", func(c *gin.Context) {
		c.JSON(http.StatusOK, cache.ListDevice())
	})

	router.GET("/device/:phone/geo", func(c *gin.Context) {
		phone := c.Param("phone")

		device, err := cache.GetDeviceByPhone(phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
			return
		}

		res := make(map[string]any)
		err = mapstructure.Decode(device, &res)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
			return
		}
		gis, err := geoCache.GetGeoLatestByPhone(phone)
		if err != nil {
			return
		}
		res["gis"] = gis

		c.JSON(http.StatusOK, res)
	})

	router.GET("/device/:phone/params", func(c *gin.Context) {
		phone := c.Param("phone")
		device, err := cache.GetDeviceByPhone(phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
			return
		}
		session, err := getDeviceActiveSession(device, phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": "Device is offline"})
			return
		}
		header := model.GenMsgHeader(device, 0x8104, session.GetNextSerialNum())
		msg := model.Msg8104{
			Header: header,
		}
		serv.Send(session.ID, &msg)
		// todo: read channel from process 0104 msg
	})

	router.PUT("/device/:phone/params", func(c *gin.Context) {
		phone := c.Param("phone")
		params := model.DeviceParams{}
		if err := c.ShouldBind(&params); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
		}
		device, err := cache.GetDeviceByPhone(phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
			return
		}
		session, err := getDeviceActiveSession(device, phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": "Device is offline"})
			return
		}
		header := model.GenMsgHeader(device, 0x8103, session.GetNextSerialNum())
		msg := model.Msg8103{
			Header:     header,
			Parameters: &params,
		}
		serv.Send(session.ID, &msg)
	})

	router.POST("/device/:phone/video/start", func(c *gin.Context) {
		phone := c.Param("phone")
		type VideoStartReq struct {
			ChannelNumber uint8  `json:"channelNumber"` // 1: Front ADAS, 2: Cabin DMS, 3: Rear, 4: Expansion
			DataType      uint8  `json:"dataType"`      // 0: AV, 1: Video
			StreamType    uint8  `json:"streamType"`    // 0: Main (1080P), 1: Sub (720P)
			ServerIP      string `json:"serverIp"`
			TCPPort       uint16 `json:"tcpPort"`
			UDPPort       uint16 `json:"udpPort"`
		}
		var req VideoStartReq
		_ = c.ShouldBindJSON(&req)

		device, err := cache.GetDeviceByPhone(phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": fmt.Sprintf("Device %s not found in registry", phone)})
			return
		}
		session, err := getDeviceActiveSession(device, phone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"err": fmt.Sprintf("Device %s is offline (no active connection on port 8080). Please ensure your dashcam is powered on and connected to the server. You can click '🧪 Test Stream' to preview the player.", phone),
			})
			return
		}

		if req.ChannelNumber == 0 {
			req.ChannelNumber = 1
		}
		if req.ServerIP == "" {
			hostParts := strings.Split(c.Request.Host, ":")
			clientHost := hostParts[0]
			if clientHost != "" && clientHost != "localhost" && clientHost != "127.0.0.1" && clientHost != "::1" {
				req.ServerIP = clientHost
			} else {
				req.ServerIP = cfg.GetPublicIP()
			}
		}
		if req.TCPPort == 0 && req.UDPPort == 0 {
			var p uint16 = 1078
			fmt.Sscanf(cfg.GetMediaPort(), "%d", &p)
			if p == 0 {
				p = 1078
			}
			req.TCPPort = p
			req.UDPPort = p
		}

		header := model.GenMsgHeader(device, model.MsgID9101, session.GetNextSerialNum())
		msg := model.Msg9101{
			Header:        header,
			ServerIP:      req.ServerIP,
			TCPPort:       req.TCPPort,
			UDPPort:       req.UDPPort,
			ChannelNumber: req.ChannelNumber,
			DataType:      req.DataType,
			StreamType:    req.StreamType,
		}
		serv.Send(session.ID, &msg)

		log.Info().
			Str("phone", phone).
			Uint8("channel", req.ChannelNumber).
			Str("serverIp", req.ServerIP).
			Uint16("tcpPort", req.TCPPort).
			Msg("Sent 0x9101 Live AV Transmission Request to device")

		c.JSON(http.StatusOK, gin.H{
			"status":        "ok",
			"msg":           "0x9101 live video stream request sent to device",
			"channelNumber": req.ChannelNumber,
			"serverIp":      req.ServerIP,
			"tcpPort":       req.TCPPort,
			"udpPort":       req.UDPPort,
			"streamUrl":     fmt.Sprintf("/live/%s_%d.flv", phone, req.ChannelNumber),
		})
	})

	router.POST("/device/:phone/video/stop", func(c *gin.Context) {
		phone := c.Param("phone")
		type VideoStopReq struct {
			ChannelNumber uint8 `json:"channelNumber"`
		}
		var req VideoStopReq
		_ = c.ShouldBindJSON(&req)
		if req.ChannelNumber == 0 {
			req.ChannelNumber = 1
		}

		device, err := cache.GetDeviceByPhone(phone)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "device not found"})
			return
		}
		session, err := getDeviceActiveSession(device, phone)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "msg": "device was already offline"})
			return
		}

		header := model.GenMsgHeader(device, model.MsgID9102, session.GetNextSerialNum())
		msg := model.Msg9102{
			Header:        header,
			ChannelNumber: req.ChannelNumber,
			ControlCmd:    0, // 关闭音视频传输
			CloseType:     0, // 关闭音视频
		}
		serv.Send(session.ID, &msg)

		log.Info().
			Str("phone", phone).
			Uint8("channel", req.ChannelNumber).
			Msg("Sent 0x9102 Live AV Transmission Close to device")

		c.JSON(http.StatusOK, gin.H{
			"status":        "ok",
			"msg":           "0x9102 close stream command sent to device",
			"channelNumber": req.ChannelNumber,
		})
	})

	router.Static("/ui", "./frontend")

	router.GET("/logs/stream", func(c *gin.Context) {
		logFile := cfg.Log.LogDirectory + cfg.Log.LogFile
		f, err := os.Open(logFile)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"err": err.Error()})
			return
		}
		defer f.Close()
		f.Seek(0, io.SeekEnd) // start from end, only new lines

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Stream(func(w io.Writer) bool {
			time.Sleep(500 * time.Millisecond)
			buf := make([]byte, 4096)
			n, _ := f.Read(buf)
			if n > 0 {
				for _, line := range strings.Split(strings.TrimSpace(string(buf[:n])), "\n") {
					if line != "" {
						c.SSEvent("log", line)
					}
				}
			}
			return true
		})
	})

	router.GET("/device/:phone/geo/stream", func(c *gin.Context) {
		phone := c.Param("phone")
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Stream(func(w io.Writer) bool {
			time.Sleep(2 * time.Second)
			gis, err := geoCache.GetGeoLatestByPhone(phone)
			if err != nil {
				return true
			}
			data, _ := json.Marshal(gis)
			c.SSEvent("location", string(data))
			return true
		})
	})

	httpAddr := ":" + cfg.Server.Port.HTTPPort

	log.Debug().Msgf("Listening and serving HTTP on :%s", cfg.Server.Port.HTTPPort)
	err := router.Run(httpAddr)
	if err != nil {
		log.Error().Err(err).Str("addr", httpAddr).Msg("Fail to run gin router")
		os.Exit(1)
	}
}
