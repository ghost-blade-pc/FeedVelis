package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"github.com/jackc/pgx/v5/pgxpool"
)

const agentBase = "/api/v1/me/agent/conversations"

func agentRequest(h *server.Hertz, token, method, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	base := []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Authorization", Value: "Bearer " + token}}
	return ut.PerformRequest(h.Engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, append(base, headers...)...)
}

func TestAgentConversationAPIAuthenticatedLifecycleAndValidation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	accounts := newAccountStack(t, env)
	ctx := context.Background()
	user, err := accounts.service.Register(ctx, accountApp.RegisterInput{Username: "agent_api", Password: "Abcd123!"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.service.Login(ctx, accountApp.LoginInput{Username: "agent_api", Password: "Abcd123!", ClientIP: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := agent.NewCursorCodec(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := newAgentService(env, agent.DefaultLimits())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	options := hertzhttp.Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: health.NewService(env.pool), Auth: &hertzhttp.AuthOptions{Service: accounts.service, Agent: handler.NewAgentConversation(service, codec, time.Hour, 4000)}}
	h := hertzhttp.NewServer(options)
	token := login.AccessToken
	key := ut.Header{Key: "Idempotency-Key", Value: agentKey(1)}
	created := agentRequest(h, token, "POST", agentBase, `{}`, key)
	if created.Code != 201 || created.Header().Get("ETag") != `"1"` {
		t.Fatalf("创建: %d %s", created.Code, created.Body.String())
	}
	var c conversation.Conversation
	if err := json.Unmarshal(created.Body.Bytes(), &c); err != nil || c.ID == "" || c.Title != "新对话" {
		t.Fatal(err)
	}
	path := agentBase + "/" + c.ID
	// 另一账户提升为管理员后仍无权访问；冲突版本及重放键不能泄露状态。
	admin, err := accounts.service.Register(ctx, accountApp.RegisterInput{Username: "agent_admin_api", Password: "Abcd123!"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.users SET role='admin' WHERE id=$1`, admin.ID.String()); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := accounts.service.Login(ctx, accountApp.LoginInput{Username: "agent_admin_api", Password: "Abcd123!", ClientIP: "203.0.113.11"})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method, path, body string
		headers            []ut.Header
	}{
		{"GET", path, "", nil}, {"GET", path + "/messages?cursor=bad", "", nil},
		{"PATCH", path, `{"title":"他人标题"}`, []ut.Header{key, {Key: "If-Match", Value: `"99"`}}},
		{"POST", path + "/messages", `{"content":"他人消息"}`, []ut.Header{key}},
		{"DELETE", path, "", nil},
	} {
		response := agentRequest(h, adminLogin.AccessToken, request.method, request.path, request.body, request.headers...)
		if response.Code != 404 || !strings.Contains(response.Body.String(), "AGENT_CONVERSATION_NOT_FOUND") {
			t.Fatalf("管理员叠加错误泄露: %d %s", response.Code, response.Body.String())
		}
	}
	replay := agentRequest(h, token, "POST", agentBase, `{}`, key)
	if replay.Code != 201 || replay.Body.String() != created.Body.String() || replay.Header().Get("ETag") != `"1"` {
		t.Fatal("201成功重放不一致")
	}
	for _, test := range []struct {
		method, path, body, code string
		headers                  []ut.Header
	}{
		{"POST", agentBase, `{"title":null}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, `{"title":"a","title":"b"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, `{"Title":"a"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, `{} {}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, `{"title":"\ud800"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, "{\"title\":\"\xff\"}", "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", agentBase, `{}`, "IDEMPOTENCY_KEY_REQUIRED", nil},
		{"POST", agentBase, `{}`, "IDEMPOTENCY_KEY_INVALID", []ut.Header{key, key}},
		{"POST", path + "/messages", `{"content":"正文","role":"assistant"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{"content":null}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{"content":12}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{"content":"\udc00"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{"content":"\u0000"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{"content":" \t\u3000"}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"POST", path + "/messages", `{}`, "VALIDATION_FAILED", []ut.Header{key}},
		{"PATCH", path, `{"title":"标题"}`, "IF_MATCH_REQUIRED", []ut.Header{key}},
		{"PATCH", path, `{"title":"标题"}`, "IF_MATCH_INVALID", []ut.Header{key, {Key: "If-Match", Value: `W/"1"`}}},
		{"PATCH", path, `{"title":"标题"}`, "IF_MATCH_INVALID", []ut.Header{key, {Key: "If-Match", Value: `"1"`}, {Key: "If-Match", Value: `"1"`}}},
		{"GET", agentBase + "?limit=0", "", "VALIDATION_FAILED", nil},
		{"GET", agentBase + "?limit=", "", "VALIDATION_FAILED", nil},
		{"GET", agentBase + "?limit=51", "", "VALIDATION_FAILED", nil},
		{"GET", agentBase + "?limit=1&limit=2", "", "VALIDATION_FAILED", nil},
		{"GET", agentBase + "?user_id=" + user.ID.String(), "", "VALIDATION_FAILED", nil},
		{"GET", agentBase + "?cursor=", "", "INVALID_CURSOR", nil},
		{"GET", agentBase + "?cursor=bad", "", "INVALID_CURSOR", nil},
		{"GET", agentBase + "/bad", "", "VALIDATION_FAILED", nil},
	} {
		r := agentRequest(h, token, test.method, test.path, test.body, test.headers...)
		if r.Code != 400 || !strings.Contains(r.Body.String(), `"`+test.code+`"`) {
			t.Fatalf("%s %s %q: %d %s", test.method, test.path, test.body, r.Code, r.Body.String())
		}
	}
	// 最坏代理对转义的合法4000字符不应被字节预算误拒绝。
	large := `{"content":"` + strings.Repeat(`\ud83d\ude00`, 4000) + `"}`
	r := agentRequest(h, token, "POST", path+"/messages", large, key)
	if r.Code != 201 {
		t.Fatalf("最大合法转义正文: %d %s", r.Code, r.Body.String())
	}
	r = agentRequest(h, token, "POST", path+"/messages", `{"content":"`+strings.Repeat("a", 65536)+`"}`, ut.Header{Key: "Idempotency-Key", Value: agentKey(2)})
	if r.Code != 400 {
		t.Fatal("超字节预算正文未拒绝")
	}
	for n := 2; n <= 65; n++ {
		r = agentRequest(h, token, "POST", path+"/messages", fmt.Sprintf(`{"content":"消息%d"}`, n), ut.Header{Key: "Idempotency-Key", Value: agentKey(n)})
		if r.Code != 201 {
			t.Fatalf("追加%d: %d %s", n, r.Code, r.Body.String())
		}
	}
	var page struct {
		Items  []conversation.Message `json:"items"`
		Cursor *string                `json:"next_cursor"`
		More   bool                   `json:"has_more"`
	}
	r = agentRequest(h, token, "GET", path+"/messages", "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || r.Code != 200 || len(page.Items) != 20 || page.Items[0].Sequence != 46 || page.Items[19].Sequence != 65 || page.Cursor == nil {
		t.Fatalf("最近历史: %d %s", r.Code, r.Body.String())
	}
	old := *page.Cursor
	r = agentRequest(h, token, "POST", path+"/messages", `{"content":"追加后"}`, ut.Header{Key: "Idempotency-Key", Value: agentKey(66)})
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	r = agentRequest(h, token, "GET", path+"/messages?limit=20&cursor="+url.QueryEscape(old), "")
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Items) != 20 || page.Items[0].Sequence != 26 || page.Items[19].Sequence != 45 {
		t.Fatal("追加后旧边界变化")
	}
	renamed := agentRequest(h, token, "PATCH", path, `{"title":"新标题"}`, key, ut.Header{Key: "If-Match", Value: `"1"`})
	if renamed.Code != 200 || renamed.Header().Get("ETag") != `"2"` {
		t.Fatalf("改名: %d %s", renamed.Code, renamed.Body.String())
	}
	r = agentRequest(h, token, "PATCH", path, `{"title":"新标题"}`, key, ut.Header{Key: "If-Match", Value: `"1"`})
	if r.Code != 200 || r.Body.String() != renamed.Body.String() {
		t.Fatal("200改名重放不一致")
	}
	// 撤销登录后，认证优先于坏正文；重新登录仍保留账户历史。
	// 会话列表默认20条，复合边界允许改变limit且不延长绝对期限。
	for n := 2; n <= 25; n++ {
		response := agentRequest(h, token, "POST", agentBase, `{}`, ut.Header{Key: "Idempotency-Key", Value: agentKey(n)})
		if response.Code != 201 {
			t.Fatal("列表种子创建失败", response.Body.String())
		}
	}
	var listPage struct {
		Items  []conversation.Conversation `json:"items"`
		Cursor *string                     `json:"next_cursor"`
		More   bool                        `json:"has_more"`
	}
	response := agentRequest(h, token, "GET", agentBase, "")
	if json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 20 || !listPage.More || listPage.Cursor == nil {
		t.Fatal("默认会话页不是20条")
	}
	firstCursor := *listPage.Cursor
	firstState, err := codec.Decode(firstCursor, user.ID.String(), agent.ListPurpose, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response = agentRequest(h, token, "GET", agentBase+"?limit=1&cursor="+url.QueryEscape(firstCursor), "")
	if json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 1 || listPage.Cursor == nil {
		t.Fatal("续页改变limit失败")
	}
	secondState, err := codec.Decode(*listPage.Cursor, user.ID.String(), agent.ListPurpose, "", time.Now())
	if err != nil || !firstState.ExpiresAt.Equal(secondState.ExpiresAt) {
		t.Fatal("列表游标续期")
	}
	response = agentRequest(h, token, "GET", agentBase+"?limit=50", "")
	if json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 25 || listPage.More || listPage.Cursor != nil {
		t.Fatal("50条边界及末页错误")
	}
	// 未返回会话活跃后可以移到边界之前；旧实时页不冻结它，首查能看到。
	if response := agentRequest(h, token, "POST", path+"/messages", `{"content":"列表活跃移动"}`, ut.Header{Key: "Idempotency-Key", Value: agentKey(100)}); response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	response = agentRequest(h, token, "GET", agentBase+"?limit=50&cursor="+url.QueryEscape(firstCursor), "")
	if json.Unmarshal(response.Body.Bytes(), &listPage) != nil {
		t.Fatal("实时续页解析失败")
	}
	for _, item := range listPage.Items {
		if item.ID == c.ID {
			t.Fatal("旧实时边界冻结了已移动会话")
		}
	}
	response = agentRequest(h, token, "GET", agentBase+"?limit=1", "")
	if json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 1 || listPage.Items[0].ID != c.ID {
		t.Fatal("刷新首查未显示活跃移动")
	}
	if err := accounts.service.Logout(ctx, login.Session.ID); err != nil {
		t.Fatal(err)
	}
	r = agentRequest(h, token, "POST", path+"/messages", `bad`)
	if r.Code != 401 {
		t.Fatal("失效身份应先401")
	}
	second, err := accounts.service.Login(ctx, accountApp.LoginInput{Username: "agent_api", Password: "Abcd123!", ClientIP: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	token = second.AccessToken
	r = agentRequest(h, token, "GET", path, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"message_count":67`) {
		t.Fatal("重登录历史丢失")
	}
	r = agentRequest(h, token, "DELETE", path, "")
	if r.Code != 204 || r.Body.Len() != 0 {
		t.Fatal("删除应204无正文")
	}
	for _, request := range []struct {
		method, path, body string
		headers            []ut.Header
	}{
		{"POST", agentBase, `{}`, []ut.Header{key}},
		{"POST", path + "/messages", large, []ut.Header{key}},
		{"PATCH", path, `{"title":"新标题"}`, []ut.Header{key, {Key: "If-Match", Value: `"1"`}}},
		{"GET", path, "", nil}, {"GET", path + "/messages", "", nil},
	} {
		r = agentRequest(h, token, request.method, request.path, request.body, request.headers...)
		if r.Code != 404 || strings.Contains(r.Body.String(), "新标题") {
			t.Fatalf("删除后重试泄露: %d %s", r.Code, r.Body.String())
		}
	}
	r = agentRequest(h, token, "DELETE", path, "")
	if r.Code != 204 {
		t.Fatal("重复删除应成功")
	}
	// 关闭再开启仅撤下路由，现存账户历史和消息必须保留。
	response = agentRequest(h, token, "GET", agentBase+"?limit=1", "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 1 {
		t.Fatal("未取得保留会话")
	}
	retainedPath := agentBase + "/" + listPage.Items[0].ID + "/messages"
	response = agentRequest(h, token, "POST", retainedPath, `{"content":"关闭后保留"}`, ut.Header{Key: "Idempotency-Key", Value: agentKey(2000)})
	if response.Code != 201 {
		t.Fatal("保留消息创建失败", response.Body.String())
	}
	activeHandler := options.Auth.Agent
	options.Auth.Agent = nil
	disabled := hertzhttp.NewServer(options)
	if response := agentRequest(disabled, token, "GET", agentBase, ""); response.Code != 404 {
		t.Fatal("关闭后路由仍可用")
	}
	options.Auth.Agent = activeHandler
	reenabled := hertzhttp.NewServer(options)
	response = agentRequest(reenabled, token, "GET", agentBase+"?limit=50", "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listPage) != nil || len(listPage.Items) != 24 {
		t.Fatalf("重新开启后历史丢失: %d %s", response.Code, response.Body.String())
	}
	response = agentRequest(reenabled, token, "GET", retainedPath, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "关闭后保留") {
		t.Fatal("重新开启后消息丢失", response.Body.String())
	}
	options.Auth.Agent = nil
	r = agentRequest(hertzhttp.NewServer(options), token, "GET", agentBase, "")
	if r.Code != 404 {
		t.Fatal("Agent关闭仍暴露路由")
	}
	options.Auth = nil
	r = agentRequest(hertzhttp.NewServer(options), token, "GET", agentBase, "")
	if r.Code != 404 {
		t.Fatal("auth关闭仍暴露路由")
	}
	// 仅断开本测试PG代理，账户认证仍使用正常连接；会话故障不能伪装404/空页。
	pc := env.pool.Config().Copy()
	proxy := redistest.NewProxy(t, net.JoinHostPort(pc.ConnConfig.Host, strconv.Itoa(int(pc.ConnConfig.Port))))
	host, port, err := net.SplitHostPort(proxy.Address())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.Host = host
	pc.ConnConfig.Port = uint16(n)
	pc.ConnConfig.ConnectTimeout = 100 * time.Millisecond
	pc.ConnConfig.Fallbacks = nil
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	proxy.Disconnect(true)
	options.Auth = &hertzhttp.AuthOptions{Service: accounts.service, Agent: handler.NewAgentConversation(newAgentService(&testEnv{pool: pool}, agent.DefaultLimits()), codec, time.Hour, 4000)}
	broken := hertzhttp.NewServer(options)
	for _, request := range []struct {
		method, path, body string
		headers            []ut.Header
	}{
		{"GET", agentBase, "", nil}, {"GET", path, "", nil}, {"GET", path + "/messages", "", nil},
		{"POST", agentBase, `{}`, []ut.Header{{Key: "Idempotency-Key", Value: agentKey(1000)}}},
	} {
		response := agentRequest(broken, token, request.method, request.path, request.body, request.headers...)
		if response.Code != 503 || !strings.Contains(response.Body.String(), "DEPENDENCY_UNAVAILABLE") {
			t.Fatalf("PG故障: %d %s", response.Code, response.Body.String())
		}
	}
	proxy.Disconnect(false)
}
