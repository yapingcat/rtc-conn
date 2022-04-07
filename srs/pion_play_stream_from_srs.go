package srs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type srsPlayRequest struct {
	Api       string `json:"api"`
	Clientip  string `json:"clientip"`
	Streamurl string `json:"streamurl"`
	Sdp       string `json:"sdp"`
	Tid       string `json:"tid"`
}

type srsPlayResponse struct {
	Code      int    `json:"code"`
	Server    string `json:"server"`
	Sdp       string `json:"sdp"`
	Sessionid string `json:"sessionid"`
}

type PionSrsPlayConnector struct {
	srsAddr        *net.TCPAddr
	app            string //live / vod ...
	streamName     string //show / tv / sport111
	tid            string //log trace
	peerConnection *webrtc.PeerConnection
	OnStateChange  func(RTCTransportState)
	OnRtp          func(cid int, pkg *rtp.Packet)
}

func NewPionSrsPlayConnector(srsAddr string, app string, streamName string) (c *PionSrsPlayConnector, e error) {
	c = &PionSrsPlayConnector{
		app:        app,
		streamName: streamName,
	}
	if c.srsAddr, e = net.ResolveTCPAddr("tcp4", srsAddr); e != nil {
		return
	}
	r := make([]byte, 8)
	if _, e = rand.Read(r); e != nil {
		return
	}
	c.tid = hex.EncodeToString(r)
	return
}

func (c *PionSrsPlayConnector) Start() (startDone chan error) {
	startDone = make(chan error, 1)
	go func() {
		defer close(startDone)

		m := &webrtc.MediaEngine{}
		var err error
		if err = m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
			PayloadType:        96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			panic(err)
		}
		if err = m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
			PayloadType:        111,
		}, webrtc.RTPCodecTypeAudio); err != nil {
			panic(err)
		}

		i := &interceptor.Registry{}

		if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
			panic(err)
		}

		// Create the API object with the MediaEngine
		api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))
		config := webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{
					URLs: []string{"stun:stun.l.google.com:19302"},
				},
			},
		}
		c.peerConnection, err = api.NewPeerConnection(config)
		if err != nil {
			startDone <- err
			return
		}

		c.peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			if s == webrtc.PeerConnectionStateFailed {
				fmt.Println("connect sucessful")
				c.stateChange(RTCTransportStateDisconnect)
			} else if s == webrtc.PeerConnectionStateConnected {
				fmt.Println("connect sucessful")
				c.stateChange(RTCTransportStateConnect)
			} else if s == webrtc.PeerConnectionStateClosed {
				fmt.Println("peerconnection closed")
				c.stateChange(RTCTransportStateDisconnect)
			} else if s == webrtc.PeerConnectionStateDisconnected {
				fmt.Println("peerconnection disconnect")
				c.stateChange(RTCTransportStateDisconnect)
			}
		})

		if _, err = c.peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			startDone <- err
			return
		} else if _, err = c.peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			startDone <- err
			return
		}

		c.peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			fmt.Println("ontrack")
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				defer ticker.Stop()
				for range ticker.C {
					errSend := c.peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if errSend != nil {
						fmt.Println(errSend)
						break
					}
				}
			}()
			c.loopRecvRtp(track)
		})

		offer := webrtc.SessionDescription{}

		offer, err = c.peerConnection.CreateOffer(nil)
		if err != nil {
			startDone <- err
			return
		}
		if err = c.peerConnection.SetLocalDescription(offer); err != nil {
			startDone <- err
			return
		}
		srsApi := "http://" + c.srsAddr.String() + "/rtc/v1/play/"
		surl := "webrtc://" + c.srsAddr.String() + "/" + c.app + "/" + c.streamName
		playreq := srsPlayRequest{
			Api:       srsApi,
			Tid:       c.tid,
			Streamurl: surl,
			Sdp:       offer.SDP,
		}

		payload, err := json.Marshal(playreq)
		if err != nil {
			startDone <- err
			return
		}
		resp, err := http.Post(srsApi, "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
		if err != nil {
			startDone <- err
			return
		}

		if resp.StatusCode != http.StatusOK {
			startDone <- err
			return
		}

		srs_json, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			startDone <- err
			return
		}
		playres := &srsPlayResponse{}
		json.Unmarshal(srs_json, playres)
		answer := webrtc.SessionDescription{}
		answer.SDP = playres.Sdp
		answer.Type = webrtc.SDPTypeAnswer
		c.peerConnection.SetRemoteDescription(answer)
		startDone <- nil
	}()
	return
}

func (c *PionSrsPlayConnector) stateChange(state RTCTransportState) {
	if c.OnStateChange != nil {
		c.OnStateChange(state)
	}
}

func (c *PionSrsPlayConnector) loopRecvRtp(track *webrtc.TrackRemote) {
	var cid int = 0
	codec := track.Codec()
	if strings.EqualFold(codec.MimeType, webrtc.MimeTypeH264) {
		cid = H264
		fmt.Println("Got H264 track, saving to disk as output.h264")
	} else if strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus) {
		cid = Opus
		fmt.Println("Got Opus track, saving to disk as output.ogg")
	} else if strings.EqualFold(codec.MimeType, webrtc.MimeTypeH265) {
		cid = H265
		fmt.Println("Got H265 track, saving to disk as output.ogg")
	} else if strings.EqualFold(codec.MimeType, webrtc.MimeTypePCMA) {
		cid = G711A
		fmt.Println("Got PCMA track, saving to disk as output.ogg")
	} else if strings.EqualFold(codec.MimeType, webrtc.MimeTypePCMU) {
		cid = G711U
		fmt.Println("Got PCMU track, saving to disk as output.ogg")
	}
	for {

		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			fmt.Printf("ReadRTP failed %s\n", err.Error())
			break
		}

		if c.OnRtp != nil {
			c.OnRtp(cid, rtpPacket)
		}
	}
}

func (c *PionSrsPlayConnector) Stop() {
	c.peerConnection.Close()
}
