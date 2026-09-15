package fritzbox

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
)

// errNoDOCSIS means the router's web interface has no cable channel data.
var errNoDOCSIS = errors.New("no DOCSIS channel data")

const emptySID = "0000000000000000"

// WebClient reads data the FRITZ!Box only offers through its web interface, such as DOCSIS
// channel values on cable models.
type WebClient struct {
	Base string // for example http://192.168.178.1
	HTTP *http.Client

	sid, user, pass string
}

type sessionInfo struct {
	SID       string `xml:"SID"`
	Challenge string `xml:"Challenge"`
	BlockTime int    `xml:"BlockTime"`
	Users     []struct {
		Name string `xml:",chardata"`
		Last int    `xml:"last,attr"`
	} `xml:"Users>User"`
}

// solveChallenge answers the login challenge: PBKDF2 (FRITZ!OS 7.24 and later) or the older MD5 scheme.
func solveChallenge(challenge, password string) (string, error) {
	if strings.HasPrefix(challenge, "2$") {
		parts := strings.Split(challenge, "$")
		if len(parts) != 5 {
			return "", fmt.Errorf("unexpected login challenge %q", challenge)
		}
		iter1, err1 := strconv.Atoi(parts[1])
		salt1, err2 := hex.DecodeString(parts[2])
		iter2, err3 := strconv.Atoi(parts[3])
		salt2, err4 := hex.DecodeString(parts[4])
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			return "", fmt.Errorf("unexpected login challenge %q: %w", challenge, err)
		}
		hash1, err := pbkdf2.Key(sha256.New, password, salt1, iter1, sha256.Size)
		if err != nil {
			return "", err
		}
		hash2, err := pbkdf2.Key(sha256.New, string(hash1), salt2, iter2, sha256.Size)
		if err != nil {
			return "", err
		}
		return parts[4] + "$" + hex.EncodeToString(hash2), nil
	}
	// Legacy: MD5 over UTF-16LE, with characters above U+00FF replaced by '.'.
	runes := []rune(challenge + "-" + password)
	for i, r := range runes {
		if r > 0xff {
			runes[i] = '.'
		}
	}
	var buf bytes.Buffer
	for _, u := range utf16.Encode(runes) {
		_ = binary.Write(&buf, binary.LittleEndian, u)
	}
	sum := md5.Sum(buf.Bytes())
	return challenge + "-" + hex.EncodeToString(sum[:]), nil
}

func (w *WebClient) sessionInfo(ctx context.Context, form url.Values) (sessionInfo, error) {
	var info sessionInfo
	method, body := http.MethodGet, io.Reader(nil)
	if form != nil {
		method, body = http.MethodPost, strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, w.Base+"/login_sid.lua?version=2", body)
	if err != nil {
		return info, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, httpStatusError(resp.StatusCode)
	}
	err = xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info)
	return info, err
}

func (w *WebClient) login(ctx context.Context, user, pass string) error {
	info, err := w.sessionInfo(ctx, nil)
	if err != nil {
		return err
	}
	if user == "" {
		// Without a user name the FRITZ!Box expects the last user that logged in.
		for _, u := range info.Users {
			if u.Last == 1 {
				user = u.Name
			}
		}
	}
	response, err := solveChallenge(info.Challenge, pass)
	if err != nil {
		return err
	}
	result, err := w.sessionInfo(ctx, url.Values{"username": {user}, "response": {response}})
	if err != nil {
		return err
	}
	if result.SID == "" || result.SID == emptySID {
		return ErrUnauthorized
	}
	w.sid = result.SID
	return nil
}

// DOCSISInfo summarises the cable modem's channels.
type DOCSISInfo struct {
	DSChannels, USChannels    int
	DSPowerMin, DSPowerMax    float64
	USPowerMax                float64
	DSMERMin                  float64 // 0 when the firmware doesn't report it
	CorrErrors, NonCorrErrors float64
}

// DocInfo logs in when needed and reads the DOCSIS channel overview.
func (w *WebClient) DocInfo(ctx context.Context, user, pass string) (DOCSISInfo, error) {
	if w.user != user || w.pass != pass {
		w.sid, w.user, w.pass = "", user, pass
	}
	for attempt := 0; attempt < 2; attempt++ {
		if w.sid == "" {
			if err := w.login(ctx, user, pass); err != nil {
				return DOCSISInfo{}, err
			}
		}
		form := url.Values{"xhr": {"1"}, "sid": {w.sid}, "lang": {"en"}, "page": {"docInfo"}, "xhrId": {"all"}, "no_sidrenew": {""}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Base+"/data.lua", strings.NewReader(form.Encode()))
		if err != nil {
			return DOCSISInfo{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := w.HTTP.Do(req)
		if err != nil {
			return DOCSISInfo{}, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return DOCSISInfo{}, err
		}
		trimmed := bytes.TrimSpace(body)
		// An expired session answers with 403 or the HTML login page instead of JSON.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized || len(trimmed) == 0 || trimmed[0] != '{' {
			w.sid = ""
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return DOCSISInfo{}, httpStatusError(resp.StatusCode)
		}
		return parseDocInfo(trimmed)
	}
	return DOCSISInfo{}, ErrUnauthorized
}

func parseDocInfo(body []byte) (DOCSISInfo, error) {
	var doc struct {
		Data struct {
			ChannelDs map[string][]map[string]any `json:"channelDs"`
			ChannelUs map[string][]map[string]any `json:"channelUs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return DOCSISInfo{}, err
	}
	var info DOCSISInfo
	dsPower, usPower := false, false
	for _, channels := range doc.Data.ChannelDs {
		for _, ch := range channels {
			info.DSChannels++
			if p, ok := number(ch["powerLevel"]); ok {
				if !dsPower || p < info.DSPowerMin {
					info.DSPowerMin = p
				}
				if !dsPower || p > info.DSPowerMax {
					info.DSPowerMax = p
				}
				dsPower = true
			}
			mer, ok := number(ch["mer"])
			if !ok {
				// DOCSIS 3.0 channels report MSE as a negative number; MER is its magnitude.
				if mse, ok2 := number(ch["mse"]); ok2 {
					mer, ok = math.Abs(mse), true
				}
			}
			if ok && mer > 0 && (info.DSMERMin == 0 || mer < info.DSMERMin) {
				info.DSMERMin = mer
			}
			if v, ok := number(ch["corrErrors"]); ok {
				info.CorrErrors += v
			}
			if v, ok := number(ch["nonCorrErrors"]); ok {
				info.NonCorrErrors += v
			}
		}
	}
	for _, channels := range doc.Data.ChannelUs {
		for _, ch := range channels {
			info.USChannels++
			if p, ok := number(ch["powerLevel"]); ok && (!usPower || p > info.USPowerMax) {
				info.USPowerMax, usPower = p, true
			}
		}
	}
	if info.DSChannels == 0 && info.USChannels == 0 {
		return info, errNoDOCSIS
	}
	return info, nil
}

// number reads JSON numbers and numeric strings, which the firmware mixes freely.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
