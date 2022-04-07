package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pion/webrtc/v3/pkg/media/h264reader"
	"github.com/yapingcat/rtc-conn/srs"
)

func main() {
	cli, _ := srs.NewPionSrsConnector("49.235.110.177:1985", "live", "show")
	tid, _ := cli.AddTrack(srs.H264)
	icedone := make(chan struct{})
	cli.OnStateChange(func(state srs.RTCTransportState) {
		if state == srs.RTCTransportStateConnect {
			fmt.Println("connect sucessful")
			close(icedone)
		}
	})

	cli.Start()

	<-icedone
	go func() {
		fmt.Println("read File")
		file, err := os.Open("output1.h264")
		if err != nil {
			panic(err)
		}

		h264, err := h264reader.NewReader(file)
		if err != nil {
			panic(err)
		}
		ticker := time.NewTicker(40 * time.Millisecond)
		for ; true; <-ticker.C {
			frame, err := h264.NextNAL()
			if err == io.EOF {
				fmt.Printf("All video frames parsed and sent")
				os.Exit(0)
			}

			if err != nil {
				panic(err)
			}
			cli.WriteFrame(tid, frame.Data, 40)
		}
	}()

	select {}
}
