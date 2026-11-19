package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kur1sulab/lance/intercept"
)

// seedReplaySnapshot 造一份「漏洞验证当时抓到的原始请求/响应」，写进证据库并绑定到漏洞,
// 让重放工具有一条真实可回放的报文。返回 snapshot id。
func seedReplaySnapshot(t *testing.T, s *Server, findingID int64, url, method, reqHead, reqBody, respBody string) string {
	t.Helper()
	store := s.evidenceStore()

	reqSum := sha256.Sum256([]byte(reqBody))
	reqHash := hex.EncodeToString(reqSum[:])
	reqPath := filepath.Join(store.Dir, "blobs", reqHash[:2], reqHash+".bin")
	if err := os.MkdirAll(filepath.Dir(reqPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reqPath, []byte(reqBody), 0o600); err != nil {
		t.Fatal(err)
	}

	respSum := sha256.Sum256([]byte(respBody))
	respHash := hex.EncodeToString(respSum[:])
	respPath := filepath.Join(store.Dir, "blobs", respHash[:2], respHash+".bin")
	if err := os.MkdirAll(filepath.Dir(respPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(respPath, []byte(respBody), 0o600); err != nil {
		t.Fatal(err)
	}

	snapID := fmt.Sprintf("replay-test-%d-%d", findingID, time.Now().UnixNano())
	if _, err := s.m.pg.Exec(`INSERT INTO traffic_evidence_snapshots
		(id, source_traffic_id, captured_at, url, method, status, content_type, req_head, resp_head, req_hash, resp_hash, req_len, resp_len)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		snapID, snapID, time.Now().UnixMilli(), url, method, 500, "application/json",
		reqHead, "HTTP/1.1 500 Internal Server Error\r\nContent-Type: application/json\r\n", reqHash, respHash,
		len(reqBody), len(respBody)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`INSERT INTO finding_traffic_bindings (finding_id, snapshot_id, role, note, position)
		VALUES ($1,$2,'request','原始验证报文',0)`, findingID, snapID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.m.pg.Exec(`DELETE FROM finding_traffic_bindings WHERE snapshot_id=$1`, snapID)
		_, _ = s.m.pg.Exec(`DELETE FROM traffic_evidence_snapshots WHERE id=$1`, snapID)
	})
	return snapID
}

// TestReplayFindingTrafficHitsTarget 证明这是真的重放：目标服务收到了一次新的 HTTP 请求，
// 而不是读取历史记录。同时验证请求体/请求头是从快照还原的。
func TestReplayFindingTrafficHitsTarget(t *testing.T) {
	s, fid := newRetestServer(t)

	var hits int32
	var gotBody, gotHeader, gotMethod string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotHeader = r.Header.Get("X-Trace")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"syntax error at or near \"1\""}`))
	}))
	defer target.Close()

	payload := `{"user":"admin' OR '1'='1","pass":"x"}`
	seedReplaySnapshot(t, s, fid, target.URL+"/api/login", "POST",
		"POST /api/login HTTP/1.1\r\nHost: "+strings.TrimPrefix(target.URL, "http://")+"\r\nX-Trace: captured\r\nContent-Type: application/json\r\n",
		payload, `{"error":"syntax error at or near \"1\""}`)

	r, conv, _, err := s.m.pg.CreateFindingRetest(t.Context(), fid, "重放验证")
	if err != nil {
		t.Fatal(err)
	}
	if r.FindingID != fid {
		t.Fatalf("finding=%d want %d", r.FindingID, fid)
	}
	ctx := intercept.WithConvID(t.Context(), conv.ID)

	out, err := s.replayFindingTraffic(ctx, json.RawMessage(`{"note":"原样重放"}`))
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("目标只收到 %d 次请求，重放未真正发出", hits)
	}
	if gotMethod != "POST" {
		t.Fatalf("method=%q", gotMethod)
	}
	if gotBody != payload {
		t.Fatalf("请求体没按快照还原: %q", gotBody)
	}
	if gotHeader != "captured" {
		t.Fatalf("快照请求头没还原: %q", gotHeader)
	}

	text := fmt.Sprint(out)
	for _, want := range []string{"500", "matches_original", "syntax error"} {
		if !strings.Contains(text, want) {
			t.Fatalf("结果里缺 %q: %s", want, text)
		}
	}
}

// TestReplayFindingTrafficOverrides 覆盖 payload 与请求头做对照验证，且拒绝换 host。
func TestReplayFindingTrafficOverrides(t *testing.T) {
	s, fid := newRetestServer(t)

	var gotBody, gotCookie string
	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	seedReplaySnapshot(t, s, fid, target.URL+"/api/user?id=1", "POST",
		"POST /api/user?id=1 HTTP/1.1\r\nHost: "+strings.TrimPrefix(target.URL, "http://")+"\r\n", `{"id":1}`, "ok")

	_, conv, _, err := s.m.pg.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := intercept.WithConvID(t.Context(), conv.ID)

	in, _ := json.Marshal(map[string]string{
		"payload_override": `{"id":"1 AND SLEEP(3)"}`,
		"header_override":  "Cookie: sid=abc123\nX-Extra: 1",
	})
	if _, err := s.replayFindingTraffic(ctx, in); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
	if gotBody != `{"id":"1 AND SLEEP(3)"}` {
		t.Fatalf("payload_override 未生效: %q", gotBody)
	}
	if gotCookie != "sid=abc123" {
		t.Fatalf("header_override 未生效: %q", gotCookie)
	}

	// 换 host 必须被拒：重放不能变成任意 SSRF 跳板。
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	bad, _ := json.Marshal(map[string]string{"url_override": other.URL + "/x"})
	res, err := s.replayFindingTraffic(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(res), "不能换协议或 host") {
		t.Fatalf("跨 host 未被拒绝: %v", res)
	}
}
