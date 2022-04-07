package main

import (
	"fmt"

	"github.com/pion/rtp"
	"github.com/yapingcat/rtc-conn/srs"
)

func main() {
	cli, _ := srs.NewPionSrsPlayConnector("49.235.110.177:1985", "live", "test")

	cli.OnStateChange = func(state srs.RTCTransportState) {
		if state == srs.RTCTransportStateConnect {
			fmt.Println("connect sucessful")
		} else if state == srs.RTCTransportStateDisconnect {
			fmt.Println("webrtc disconnect")
			cli.Stop()
		}
	}

	cli.OnRtp = func(cid int, pkg *rtp.Packet) {
		if cid == srs.H264 {
			fmt.Printf("recv H264 Rtp packet pkg len :%d\n", len(pkg.Payload))
		} else if cid == srs.Opus {
			fmt.Printf("recv opus Rtp packet pkg len :%d\n", len(pkg.Payload))
		}
	}

	cli.Start()

	select {}
}
