package mtu

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/loehnertz/LANdlord/internal/collect"
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/record"
)

var dst = netip.MustParseAddr("1.1.1.1")

func limitedTo(payload int) *platform.Fake {
	return &platform.Fake{EchoFunc: func(req platform.EchoRequest) (platform.EchoReply, error) {
		if req.Size > payload {
			return platform.EchoReply{Status: platform.EchoTooBig}, nil
		}
		return platform.EchoReply{From: req.Dst, Status: platform.EchoOK}, nil
	}}
}

func TestDiscover(t *testing.T) {
	for payload, want := range map[int]int{1452: 1480, 1472: 1500, 1432: 1460} {
		pg, _ := limitedTo(payload).NewPinger(4)
		got, err := Discover(context.Background(), pg, dst)
		if err != nil || got != want {
			t.Fatalf("payload %d: Discover = %d, %v; want %d", payload, got, err, want)
		}
	}
	pg, _ := limitedTo(1000).NewPinger(4)
	if _, err := Discover(context.Background(), pg, dst); !errors.Is(err, errTooSmall) {
		t.Fatalf("expected errTooSmall, got %v", err)
	}
}

func TestCollectorEmitsAndUnsupportedIsPermanent(t *testing.T) {
	c := New(limitedTo(1452), dst, time.Hour)
	var buf record.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- c.Run(ctx, &buf) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(buf.Filter(record.CMTU, record.NPMTU)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if recs := buf.Filter(record.CMTU, record.NPMTU); len(recs) != 1 || recs[0].Values["mtu"] != 1480 {
		t.Fatalf("records = %+v", recs)
	}

	noDF := &platform.Fake{EchoFunc: func(platform.EchoRequest) (platform.EchoReply, error) {
		return platform.EchoReply{}, platform.ErrUnsupported
	}}
	if err := New(noDF, dst, time.Hour).Run(context.Background(), &buf); !errors.Is(err, collect.ErrPermanent) {
		t.Fatalf("expected permanent error, got %v", err)
	}
}
