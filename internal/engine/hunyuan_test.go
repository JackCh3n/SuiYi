package engine

import (
	"context"
	"os"
	"strings"
	"testing"
)

// ---------- 离线单元测试（不联网，CI 也会跑） ----------

func TestHunyuanBuildBody(t *testing.T) {
	h := NewHunyuan("", "", "11111111-2222-4333-8444-555555555555")

	// 白名单风格直传 style
	b := h.buildBody("你好", "auto", "en", "", "daily_spoken", false)
	if b.Style != "daily_spoken" || b.UserPrompts != "" {
		t.Fatalf("白名单风格应直传 style，得到 style=%q prompts=%q", b.Style, b.UserPrompts)
	}
	if b.SourceLanguage != "auto" || b.Stream {
		t.Fatalf("源语言/流式标志不对: %+v", b)
	}

	// 自由文本风格 → user_prompts（接口 style 是严格白名单，传错会 400）
	b = h.buildBody("你好", "zh", "en", "", "商务正式", false)
	if b.Style != "" || !strings.Contains(b.UserPrompts, "商务正式") {
		t.Fatalf("自由风格应走 user_prompts，得到 %+v", b)
	}

	// 术语表 → user_prompts，且与风格合并
	b = h.buildBody("你好", "zh", "en", "API=接口", "商务正式", false)
	if !strings.Contains(b.UserPrompts, "商务正式") || !strings.Contains(b.UserPrompts, "API=接口") {
		t.Fatalf("风格与术语应合并进 user_prompts，得到 %q", b.UserPrompts)
	}

	// 语言码修正：接口用 zh-TW，不认 zh-Hant
	if got := h.buildBody("hi", "auto", "zh-Hant", "", "", false).TargetLanguage; got != "zh-TW" {
		t.Fatalf("zh-Hant 应映射为 zh-TW，得到 %q", got)
	}
	// 目标语言为空时兜底 zh；源语言为空时用 auto
	b = h.buildBody("hi", "", "", "", "", false)
	if b.TargetLanguage != "zh" || b.SourceLanguage != "auto" {
		t.Fatalf("空语言兜底不对: %+v", b)
	}
}

func TestHunyuanAnonModeFlag(t *testing.T) {
	if !NewHunyuan("", "", "").Anonymous() {
		t.Fatal("未配置凭证应为匿名模式")
	}
	if NewHunyuan("u_0000000000000000", "tok", "").Anonymous() {
		t.Fatal("配置了账号凭证不应为匿名模式")
	}
	// 关键回归：匿名登录拿到凭证后，仍必须被视为匿名模式（否则 401 不会自动重登）
	h := NewHunyuan("", "", "")
	h.userID, h.token = "a_deadbeef", "anon-token"
	if !h.Anonymous() {
		t.Fatal("持有匿名凭证时仍应视为匿名模式")
	}
}

func TestHunyuanDeviceIDStable(t *testing.T) {
	h1 := NewHunyuan("", "", "")
	h2 := NewHunyuan("", "", "")
	if h1.DeviceID() == "" || h1.DeviceID() == h2.DeviceID() {
		t.Fatal("未指定设备号时应各自生成随机 UUID")
	}
	if got := NewHunyuan("", "", "fixed-device-id").DeviceID(); got != "fixed-device-id" {
		t.Fatalf("指定设备号应原样使用，得到 %q", got)
	}
}

func TestReadableErr(t *testing.T) {
	if got := readableErr([]byte(`{"error":{"code":"20001","message":"token无效"}}`)); got != "[20001] token无效" {
		t.Fatalf("网关错误解析不对: %q", got)
	}
	if got := readableErr([]byte(`{"code":12000,"message":"style 非法"}`)); got != "[12000] style 非法" {
		t.Fatalf("业务错误解析不对: %q", got)
	}
}

// ---------- 联网测试（默认跳过，SUIYI_LIVE_TEST=1 时启用） ----------

func live(t *testing.T) {
	if os.Getenv("SUIYI_LIVE_TEST") == "" {
		t.Skip("需要联网调用混元接口，设 SUIYI_LIVE_TEST=1 启用")
	}
}

// 匿名凭证失效（如过期/被回收）后应自动重新匿名登录并完成翻译
func TestLiveHunyuanAnonRecovery(t *testing.T) {
	live(t)
	h := NewHunyuan("", "", "")
	h.userID, h.token = "a_stale", "stale-invalid-token" // 模拟已失效的匿名凭证
	out, err := h.TranslateText(context.Background(), "今天天气不错，我们出去走走吧。", "auto", "en", "", "")
	if err != nil {
		t.Fatalf("失效后应自动重登并翻译成功: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("译文为空")
	}
	t.Logf("自愈后译文: %s", out)
}

// 账号模式令牌无效时应如实报错，不应尝试匿名重登（否则会悄悄换成匿名身份）
func TestLiveHunyuanAccountModeNoRetry(t *testing.T) {
	live(t)
	h := NewHunyuan("u_0000000000000000", "invalid-account-token", "")
	_, err := h.TranslateText(context.Background(), "hi", "auto", "en", "", "")
	if err == nil {
		t.Fatal("账号令牌无效时应报错")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "token") {
		t.Fatalf("错误信息应可读: %v", err)
	}
	t.Logf("账号模式错误（预期）: %v", err)
}
