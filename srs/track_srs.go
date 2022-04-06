package srs

import (
	"strings"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

func payloaderForCodec(codec webrtc.RTPCodecCapability) (rtp.Payloader, error) {
	switch strings.ToLower(codec.MimeType) {
	case strings.ToLower(webrtc.MimeTypeH264):
		return &codecs.H264Payloader{}, nil
	case strings.ToLower(webrtc.MimeTypeOpus):
		return &codecs.OpusPayloader{}, nil
	case strings.ToLower(webrtc.MimeTypeVP8):
		return &codecs.VP8Payloader{
			EnablePictureID: true,
		}, nil
	case strings.ToLower(webrtc.MimeTypeVP9):
		return &codecs.VP9Payloader{}, nil
	case strings.ToLower(webrtc.MimeTypeG722):
		return &codecs.G722Payloader{}, nil
	case strings.ToLower(webrtc.MimeTypePCMU), strings.ToLower(webrtc.MimeTypePCMA):
		return &codecs.G711Payloader{}, nil
	default:
		return nil, webrtc.ErrNoPayloaderForCodec
	}
}

type TrackSRSStaticSample struct {
	packetizer rtp.Packetizer
	sequencer  rtp.Sequencer
	rtpTrack   *webrtc.TrackLocalStaticRTP
	clockRate  float64
	mtx        sync.Mutex
	hookRtp    func(*rtp.Packet)
}

func NewTrackSRSStaticSample(c webrtc.RTPCodecCapability, id, streamID string, options ...func(*webrtc.TrackLocalStaticRTP)) (*TrackSRSStaticSample, error) {
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(c, id, streamID, options...)
	if err != nil {
		return nil, err
	}

	return &TrackSRSStaticSample{
		rtpTrack: rtpTrack,
	}, nil
}

func (s *TrackSRSStaticSample) ID() string { return s.rtpTrack.ID() }

func (s *TrackSRSStaticSample) StreamID() string { return s.rtpTrack.StreamID() }

func (s *TrackSRSStaticSample) RID() string { return s.rtpTrack.RID() }

func (s *TrackSRSStaticSample) Kind() webrtc.RTPCodecType { return s.rtpTrack.Kind() }

func (s *TrackSRSStaticSample) Codec() webrtc.RTPCodecCapability {
	return s.rtpTrack.Codec()
}

func (s *TrackSRSStaticSample) Bind(t webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	codec, err := s.rtpTrack.Bind(t)
	if err != nil {
		return codec, err
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()

	if s.packetizer != nil {
		return codec, nil
	}

	payloader, err := payloaderForCodec(codec.RTPCodecCapability)
	if err != nil {
		return codec, err
	}

	s.sequencer = rtp.NewRandomSequencer()
	s.packetizer = rtp.NewPacketizer(
		1200,
		0,
		0,
		payloader,
		s.sequencer,
		codec.ClockRate,
	)
	s.clockRate = float64(codec.RTPCodecCapability.ClockRate)
	return codec, nil
}

func (s *TrackSRSStaticSample) Unbind(t webrtc.TrackLocalContext) error {
	return s.rtpTrack.Unbind(t)
}

func (s *TrackSRSStaticSample) WriteSample(sample media.Sample) error {
	s.mtx.Lock()
	p := s.packetizer
	clockRate := s.clockRate
	s.mtx.Unlock()

	if p == nil {
		return nil
	}

	// skip packets by the number of previously dropped packets
	for i := uint16(0); i < sample.PrevDroppedPackets; i++ {
		s.sequencer.NextSequenceNumber()
	}

	samples := uint32(sample.Duration.Seconds() * clockRate)
	if sample.PrevDroppedPackets > 0 {
		p.SkipSamples(samples * uint32(sample.PrevDroppedPackets))
	}
	packets := p.Packetize(sample.Data, samples)

	for _, p := range packets {
		if s.hookRtp != nil {
			s.hookRtp(p)
		}
		s.mtx.Lock()
		defer s.mtx.Unlock()
		if err := s.rtpTrack.WriteRTP(p); err != nil {
			return err
		}
	}

	return nil
}

func (s *TrackSRSStaticSample) WriteRtpPacket(pkg *rtp.Packet) error {
	s.mtx.Lock()
	if err := s.rtpTrack.WriteRTP(pkg); err != nil {
		return err
	}
	s.mtx.Unlock()
	return nil
}
