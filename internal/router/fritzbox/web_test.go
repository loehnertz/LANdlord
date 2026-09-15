package fritzbox

import (
	"context"
	"errors"
	"testing"
)

func TestSolveChallenge(t *testing.T) {
	// Examples from AVM's "Session-IDs im FRITZ!Box Webinterface" documentation.
	got, err := solveChallenge("2$10000$5A1711$2000$5A1722", "1example!")
	if err != nil || got != "5A1722$1798a1672bca7c6463d6b245f82b53703b0f50813401b03e4045a5861e689adb" {
		t.Fatalf("PBKDF2 response = %s, %v", got, err)
	}
	got, err = solveChallenge("1234567z", "äbc")
	if err != nil || got != "1234567z-9e224a41eeefa284df7bb0f26c2913e2" {
		t.Fatalf("MD5 response = %s, %v", got, err)
	}
	if _, err := solveChallenge("2$bad", "x"); err == nil {
		t.Fatal("malformed challenge should fail")
	}
}

const docInfoFixture = `{"pid":"docInfo","data":{
 "channelDs":{
  "docsis30":[
   {"type":"256QAM","corrErrors":12,"mse":"-38.6","powerLevel":"4.9","channel":1,"nonCorrErrors":3,"latency":0.32,"channelID":5,"frequency":"346"},
   {"type":"256QAM","corrErrors":8,"mse":"-35.1","powerLevel":"-2.3","channel":2,"nonCorrErrors":1,"latency":0.32,"channelID":6,"frequency":"354"}],
  "docsis31":[
   {"powerLevel":"6.4","type":"4096QAM","channel":33,"channelID":33,"plc":"202","mer":"42","fft":"4K","frequency":"134-325"}]},
 "channelUs":{
  "docsis30":[
   {"powerLevel":"44.3","type":"64QAM","channel":1,"multiplex":"ATDMA","channelID":2,"frequency":"30.8"},
   {"powerLevel":47.5,"type":"64QAM","channel":2,"multiplex":"ATDMA","channelID":3,"frequency":"37.2"}],
  "docsis31":[
   {"powerLevel":"41.5","type":"OFDMA","channel":1,"channelID":1,"frequency":"29.8-64.8","activesub":"1880","fft":"2K"}]}}}`

func TestParseDocInfo(t *testing.T) {
	info, err := parseDocInfo([]byte(docInfoFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := DOCSISInfo{DSChannels: 3, USChannels: 3, DSPowerMin: -2.3, DSPowerMax: 6.4, USPowerMax: 47.5, DSMERMin: 35.1, CorrErrors: 20, NonCorrErrors: 4}
	if info != want {
		t.Fatalf("info = %+v\nwant   %+v", info, want)
	}
	if _, err := parseDocInfo([]byte(`{"data":{"channelDs":{},"channelUs":{}}}`)); !errors.Is(err, errNoDOCSIS) {
		t.Fatalf("empty channel lists should be errNoDOCSIS, got %v", err)
	}
	_ = context.Background
}
