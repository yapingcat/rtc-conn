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
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

const (
	H264 = iota + 1
	H265
	Opus
	G711A
	G711U
)

type RTCTransportState int

const (
	RTCTransportStateInit RTCTransportState = iota + 1
	RTCTransportStateConnecting
	RTCTransportStateConnect
	RTCTransportStateDisconnect
	RTCTransportStateFailed
)

func (s RTCTransportState) String() string {
	switch s {
	case RTCTransportStateInit:
		return "Init"
	case RTCTransportStateConnecting:
		return "Connecting"
	case RTCTransportStateConnect:
		return "Connected"
	case RTCTransportStateDisconnect:
		return "Disconnect"
	case RTCTransportStateFailed:
		return "Failed"
	}
	return ""
}

type srsPushRequest struct {
	Api       string `json:"api"`
	Clientip  string `json:"clientip"`
	Streamurl string `json:"streamurl"`
	Sdp       string `json:"sdp"`
	Tid       string `json:"tid"`
}

type srsPushResponse struct {
	Code      int    `json:"code"`
	Server    string `json:"server"`
	Sdp       string `json:"sdp"`
	Sessionid string `json:"sessionid"`
}

type rtcTrack struct {
	track    *TrackSRSStaticSample
	sender   *webrtc.RTPSender
	sendList []*rtp.Packet
}

func (t *rtcTrack) loopRecvRtcp() {
	for {
		if rtcppkgs, _, rtcpErr := t.sender.ReadRTCP(); rtcpErr != nil {
			return
		} else {
			for _, pkg := range rtcppkgs {
				if stringer, canString := pkg.(fmt.Stringer); canString {
					fmt.Printf("Received RTCP Packet: %v", stringer.String())
				}
				switch v := pkg.(type) {
				case *rtcp.TransportLayerNack:
					fmt.Printf("recv NACK %d\n", v.MediaSSRC)
					for _, nack := range v.Nacks {
						seqIds := nack.PacketList()
						for _, id := range seqIds {
							for _, pkg := range t.sendList {
								if pkg.SequenceNumber == id {
									t.track.WriteRtpPacket(pkg)
								}
							}
						}
						fmt.Printf("recv nack pkg id %d\n", nack.PacketID)
					}
				case *rtcp.PictureLossIndication:
					fmt.Printf("recv PLI %d\n", v.MediaSSRC)
				case *rtcp.ReceiverReport:
					fmt.Printf("recv RR %d\n", v.SSRC)
				default:
					fmt.Println("recv other")
				}
			}
		}
	}
}

type PionSrsPushConnector struct {
	srsAddr        *net.TCPAddr
	app            string //live / vod ...
	streamName     string //show / tv / sport111
	tid            string //log trace
	peerConnection *webrtc.PeerConnection
	tracks         map[int]*rtcTrack
	nextTrackId    int
	OnStateChange  func(RTCTransportState)
}

func NewPionSrsPushConnector(srsAddr string, app string, streamName string) (c *PionSrsPushConnector, e error) {
	c = &PionSrsPushConnector{
		app:         app,
		streamName:  streamName,
		tracks:      make(map[int]*rtcTrack),
		nextTrackId: 0,
	}
	if c.srsAddr, e = net.ResolveTCPAddr("tcp4", srsAddr); e != nil {
		return
	}
	r := make([]byte, 8)
	if _, e = rand.Read(r); e != nil {
		return
	}
	c.tid = hex.EncodeToString(r)
	if c.peerConnection, e = webrtc.NewPeerConnection(webrtc.Configuration{}); e != nil {
		return
	}
	return
}

//
func (c *PionSrsPushConnector) AddTrack(cid int) (id int, e error) {
	t := &rtcTrack{}

	t.track, e = NewTrackSRSStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "srs")
	if e != nil {
		return
	}

	t.track.hookRtp = func(p *rtp.Packet) {
		t.sendList = append(t.sendList, p)
		idx := 0
		for (t.sendList[idx].Timestamp-t.sendList[len(t.sendList)-1].Timestamp)*1000/t.track.Codec().ClockRate > 500 {
			idx++
		}

		if idx == 0 {
			return
		}
		tmplist := make([]*rtp.Packet, 0, cap(t.sendList))
		copy(tmplist, t.sendList[idx+1:])
		t.sendList = tmplist
	}

	t.sender, e = c.peerConnection.AddTrack(t.track)
	if e != nil {
		return
	}

	c.tracks[c.nextTrackId] = t
	id = c.nextTrackId
	c.nextTrackId++
	return
}

func (c *PionSrsPushConnector) Start() error {

	c.peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed {
			c.stateChange(RTCTransportStateDisconnect)
		} else if s == webrtc.PeerConnectionStateConnected {
			for _, track := range c.tracks {
				go track.loopRecvRtcp()
			}
			c.stateChange(RTCTransportStateConnect)
		} else if s == webrtc.PeerConnectionStateClosed {
			fmt.Println("peerconnection closed")
			c.stateChange(RTCTransportStateDisconnect)
		} else if s == webrtc.PeerConnectionStateDisconnected {
			fmt.Println("peerconnection disconnect")
			c.stateChange(RTCTransportStateDisconnect)
		}
	})

	offer, err := c.peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	if err = c.peerConnection.SetLocalDescription(offer); err != nil {
		return err
	}

	go func() {
		srsApi := "http://" + c.srsAddr.String() + "/rtc/v1/publish/"
		surl := "webrtc://" + c.srsAddr.String() + "/" + c.app + "/" + c.streamName
		pushreq := srsPushRequest{
			Api:       srsApi,
			Tid:       c.tid,
			Streamurl: surl,
			Sdp:       offer.SDP,
		}

		payload, err := json.Marshal(pushreq)
		if err != nil {
			c.stateChange(RTCTransportStateFailed)
			return
		}
		resp, err := http.Post(srsApi, "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
		if err != nil {
			c.stateChange(RTCTransportStateFailed)
			return
		}

		if resp.StatusCode != http.StatusOK {
			c.stateChange(RTCTransportStateFailed)
			return
		}

		srs_json, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			c.stateChange(RTCTransportStateFailed)
			return
		}
		pushres := &srsPushResponse{}
		json.Unmarshal(srs_json, pushres)
		answer := webrtc.SessionDescription{}
		answer.SDP = pushres.Sdp
		answer.Type = webrtc.SDPTypeAnswer
		c.peerConnection.SetRemoteDescription(answer)
	}()

	return nil
}

func (c *PionSrsPushConnector) Stop() {
	c.peerConnection.Close()
}

func (c *PionSrsPushConnector) WriteFrame(trackid int, data []byte, duration int64) error {
	track := c.tracks[trackid]
	track.track.WriteSample(media.Sample{Data: data, Duration: time.Millisecond * time.Duration(duration)})
	return nil
}

func (c *PionSrsPushConnector) stateChange(state RTCTransportState) {
	if c.OnStateChange != nil {
		c.OnStateChange(state)
	}
}
