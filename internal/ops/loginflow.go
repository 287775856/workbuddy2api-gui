// loginflow.go 把「登录会话」与「凭证落盘 + 容器重启」串起来。
// LoginManager 只负责 OAuth 轮询；落盘与重启需要 store / docker 能力，故放在 Service 上。
package ops

import (
	"context"
	"fmt"
	"strings"

	"workbuddy2api-gui/internal/authstore"
	"workbuddy2api-gui/internal/upstream"
)

// StartLogin 发起一次网页登录，返回含授权 URL 的会话。
func (s *Service) StartLogin(region upstream.Region) (*LoginSession, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	return s.logins.Start(region)
}

// PollLogin 轮询登录会话；一旦上游返回凭证，立即落盘并按配置重启容器。
//
// 落盘成功才标记 Saved=true；重启失败不回滚落盘（凭证已有效，用户可手动重启），
// 但把原因写进 Restart 字段让前端展示。
func (s *Service) PollLogin(ctx context.Context, id string) (*LoginSession, error) {
	sess, err := s.logins.Poll(id)
	if err != nil {
		return nil, err
	}
	if sess.Status != LoginSuccess {
		return sess, nil
	}
	acct := s.logins.takeAccount(id)
	if acct == nil {
		// 已被本次或前一次轮询处理过，直接返回当前快照。
		return sess, nil
	}

	// 落盘：先设定目标路径再 Save（Save 会用 FilePath，为空时按 uid 推导）。
	if err := s.store.Save(acct); err != nil {
		s.logins.markSaved(id, "", "凭证保存失败: "+err.Error())
		return s.logins.Get(id)
	}
	file := baseName(acct.FilePath)
	restartNote := ""

	// 按配置重启容器以加载新账号。
	if s.cfg.RefreshConfigOnLogin && s.cfg.DangerousOps && s.cfg.DockerContainer != "" {
		if msg, rerr := s.RestartContainer(ctx); rerr != nil {
			restartNote = "凭证已保存，但重启容器失败，请手动重启：" + rerr.Error()
		} else {
			restartNote = msg + "，新账号已加载"
		}
	} else {
		switch {
		case s.cfg.DockerContainer == "":
			restartNote = "凭证已保存；未配置容器名，请手动重启网关使其加载"
		case !s.cfg.DangerousOps:
			restartNote = "凭证已保存；自动重启需要开启 dangerous_ops，请手动重启网关使其加载"
		default:
			restartNote = "凭证已保存；自动重启已关闭，请手动重启网关使其加载"
		}
	}
	s.logins.markSaved(id, file, restartNote)
	return s.logins.Get(id)
}

// CancelLogin 取消登录会话。
func (s *Service) CancelLogin(id string) (*LoginSession, error) {
	return s.logins.Cancel(id)
}

// SaveManualAuth 手工导入凭证（粘贴 accessToken/refreshToken 或整个 JSON 文件）。
//
// 用途：账号已通过 login.sh 或插件登录过，只想把凭证塞进 auths/ 而不想重走 OAuth；
// 或从别的机器迁移账号。支持两种输入：
//  1. 完整凭证 JSON（嵌套形/扁平形均可）
//  2. 单独粘贴 accessToken + refreshToken + uid
func (s *Service) SaveManualAuth(input ManualAuthInput) (*OpResult, error) {
	if err := s.ensureWritable(); err != nil {
		return nil, err
	}
	var acct *authstore.Account
	if strings.TrimSpace(input.RawJSON) != "" {
		parsed, err := authstore.Parse([]byte(input.RawJSON))
		if err != nil {
			return &OpResult{Action: "import", OK: false, Message: "凭证解析失败: " + err.Error()}, nil
		}
		acct = parsed
	} else {
		if strings.TrimSpace(input.AccessToken) == "" {
			return &OpResult{Action: "import", OK: false, Message: "accessToken 不能为空"}, nil
		}
		acct = &authstore.Account{
			AccessToken:  strings.TrimSpace(input.AccessToken),
			RefreshToken: strings.TrimSpace(input.RefreshToken),
			UID:          strings.TrimSpace(input.UID),
			Nickname:     strings.TrimSpace(input.Nickname),
			EnterpriseID: strings.TrimSpace(input.EnterpriseID),
			Domain:       strings.TrimSpace(input.Domain),
			ExpiresAt:    input.ExpiresAt,
		}
	}
	if acct.UID == "" {
		return &OpResult{Action: "import", OK: false, Message: "缺少 uid：无法确定凭证文件名，请补全 uid 字段"}, nil
	}
	if !authstore.ValidUID(acct.UID) {
		return &OpResult{Action: "import", OK: false,
			Message: fmt.Sprintf("uid 含非法字符（仅允许字母数字 . _ -）：%q", acct.UID)}, nil
	}
	if acct.Domain == "" {
		acct.Domain = "copilot.tencent.com"
	}
	// 过期时间缺失时给一个保守值（1 小时后），触发首次使用前自动刷新。
	if acct.ExpiresAt <= 0 {
		acct.ExpiresAt = 0
	}
	if err := s.store.Save(acct); err != nil {
		return &OpResult{UID: acct.UID, Action: "import", OK: false, Message: "保存失败: " + err.Error()}, nil
	}
	return &OpResult{
		UID: acct.UID, Action: "import", OK: true,
		Message: fmt.Sprintf("凭证已保存到 %s；重启网关后生效", baseName(acct.FilePath)),
		Data:    map[string]any{"file": baseName(acct.FilePath)},
	}, nil
}

// ManualAuthInput 手工导入凭证的输入。
type ManualAuthInput struct {
	RawJSON      string `json:"raw_json"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UID          string `json:"uid"`
	Nickname     string `json:"nickname"`
	EnterpriseID string `json:"enterprise_id"`
	Domain       string `json:"domain"`
	ExpiresAt    int64  `json:"expires_at"`
}
