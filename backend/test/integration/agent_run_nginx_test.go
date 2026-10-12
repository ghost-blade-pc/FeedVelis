package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	orch "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentorchestration"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	eino "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nginxAgentProxy(t *testing.T, upstream string) string {
	t.Helper()
	image := os.Getenv("VELIS_TEST_NGINX_IMAGE")
	if image == "" {
		t.Skip("未设置 VELIS_TEST_NGINX_IMAGE，跳过实际 nginx 验收")
	}
	body, err := os.ReadFile("../../../web/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	containerUpstream := strings.Replace(upstream, "127.0.0.1", "host.docker.internal", 1)
	conf := strings.ReplaceAll(string(body), "http://velis-api:8080", containerUpstream)
	path := filepath.Join(t.TempDir(), "default.conf")
	if err := os.WriteFile(path, []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("velis-agent-nginx-test-%d", time.Now().UnixNano())
	cmd := exec.CommandContext(t.Context(), "docker", "run", "--rm", "-d", "-p", fmt.Sprintf("127.0.0.1:%d:8080", port), "--add-host", "host.docker.internal:host-gateway", "--name", name, "-v", path+":/etc/nginx/conf.d/default.conf:ro", "--entrypoint", "nginx", image, "-g", "daemon off;")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("启动隔离 nginx: %s %v", output, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return base
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
	t.Fatalf("nginx 未就绪: %s", logs)
	return ""
}

func TestAgentRunNginxModelStreamingAndReconnect(t *testing.T) {
	// 模型始终是本机确定性桩，不读取收费 Provider 配置。
	env := newTestEnv(t)
	seedAgentSessions(t, env)
	seedFeedbackArticle(t, env)
	c, m := agentQuestion(t, env, 1)
	var calls atomic.Int32
	release := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Authorization") != "Bearer local-test-only" {
			http.Error(w, "invalid", 400)
			return
		}
		if !req.Stream {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": `{"constraints":{},"clarification":"","require_body":true}`}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			return
		}
		var input struct {
			Evidence []orch.Material `json:"evidence"`
		}
		_ = json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &input)
		if len(input.Evidence) == 0 {
			http.Error(w, "no evidence", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(content, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		send("{\"type\":\"header\",\"version\":1,\"kind\":\"answer\"}\n", "")
		for i := 1; i <= 2; i++ {
			raw, _ := json.Marshal(map[string]any{"type": "block", "id": fmt.Sprint("b", i), "text": "文章正文解释", "evidence_ids": []string{input.Evidence[0].ID}})
			send(string(raw)+"\n", "")
			select {
			case <-r.Context().Done():
				return
			case <-time.After(600 * time.Millisecond):
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		send("{\"type\":\"finish\"}\n", "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer model.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cfg := config.Default().Agent
	cfg.Generation.BaseURL = model.URL + "/v1"
	cfg.Generation.Model = "local-reading-stub"
	cfg.Generation.APIKey = config.SecretBytes("local-test-only")
	cfg.Generation.Timeout = 30 * time.Second
	repo := postgres.NewAgentRunRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	guard := agent.NewGuard(repo, repo, tx)
	facts := postgres.NewAgentEvidenceFacts(env.pool)
	tools := agenttools.NewService(nil, nil, postgres.NewArticleRepository(env.pool), agenttools.Config{Timeout: time.Second, MaxOutputBytes: 128 * 1024})
	runner := eino.NewReadingRunner(cfg, tools, guard, facts)
	_, s := startExecutor(t, env, runner, true)
	upstream, _ := runHTTPServer(t, s, time.Now().Add(time.Minute), "0.0.0.0:0")
	base := nginxAgentProxy(t, upstream)
	article := int64(7201)
	accepted, err := s.Accept(t.Context(), agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID, ContextArticleID: &article})
	if err != nil {
		t.Fatal(err)
	}
	scope := agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}
	started := time.Now()
	response := runHTTPRequest(t, base, c.ID, scope.RunID, "")
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal(response.StatusCode, response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	var position string
	first := time.Time{}
	for {
		id, _, event := readRunEvent(t, scanner)
		if event.Kind == agent.AnswerBlock {
			position = id
			first = time.Now()
			record, _ := repo.Find(t.Context(), scope)
			if record.Run.State != run.Running {
				t.Fatal("代理缓存至终态", record.Run.State)
			}
			response.Body.Close()
			detail, err := s.Detail(t.Context(), agentCaller, c.ID, scope.RunID)
			if err != nil || detail.Answer == nil || len(detail.Answer.Blocks) < 1 {
				t.Fatal(detail, err)
			}
			break
		}
	}
	// 详情替换快照后从第一次片段的位置续读同一个回合。
	response = runHTTPRequest(t, base, c.ID, scope.RunID, position)
	defer response.Body.Close()
	scanner = bufio.NewScanner(response.Body)
	second := time.Time{}
	heartbeat := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var event agent.Event
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)
			if event.Kind == agent.AnswerBlock {
				second = time.Now()
				if event.Block.ID != "b2" {
					t.Fatal("位置去重失败", event)
				}
			}
		}
		if strings.HasPrefix(line, ":") && !second.IsZero() && time.Since(second) > 9*time.Second {
			heartbeat = true
			break
		}
	}
	if second.IsZero() || !heartbeat {
		t.Fatal("代理未及时传递第二片段或心跳", scanner.Err())
	}
	record, _ := repo.Find(t.Context(), scope)
	if record.Run.State != run.Running {
		t.Fatal(record.Run.State)
	}
	close(release)
	record = awaitRun(t, repo, scope, run.Succeeded, 3*time.Second)
	var assistants int
	_ = env.pool.QueryRow(t.Context(), `SELECT count(*) FROM velis.agent_messages WHERE role='assistant'`).Scan(&assistants)
	if calls.Load() != 2 || assistants != 1 || record.Run.AssistantReserved || record.Answer == nil || len(record.Answer.Blocks) != 2 {
		t.Fatal(calls.Load(), assistants, record)
	}
	t.Logf("实际 nginx：首片段=%v，第二片段=%v，终态=%v；数据库状态=%s，已提交事件=%d，片段=%d，模型调用=%d，工具调用=%d，助手=%d，心跳=%v，响应头=%v", first.Sub(started), second.Sub(started), time.Since(started), record.Run.State, record.LastEventSequence, len(record.Answer.Blocks), calls.Load(), record.Usage.ToolCalls, assistants, heartbeat, response.Header)
}
