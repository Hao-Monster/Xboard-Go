package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const legacyTelegramPluginReadme = `# Telegram 插件

受信内置的 Telegram Bot 功能，提供账号绑定、流量查询、订阅链接获取、工单回复和管理员通知。

## 可用命令

- /start：显示欢迎与帮助信息
- /bind [订阅链接]：绑定 XBoard 账号
- /traffic：查询当前账号流量
- /getlatesturl：获取最新订阅链接
- /unbind：解除 Telegram 绑定

工单通知、支付通知与欢迎/帮助文案可在插件配置中调整。`

type legacyTrustedPluginResponse struct {
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Description  string    `json:"description"`
	Author       string    `json:"author"`
	Type         string    `json:"type"`
	IsInstalled  bool      `json:"is_installed"`
	IsEnabled    bool      `json:"is_enabled"`
	IsProtected  bool      `json:"is_protected"`
	CanBeDeleted bool      `json:"can_be_deleted"`
	Config       any       `json:"config"`
	Readme       string    `json:"readme"`
	NeedUpgrade  bool      `json:"need_upgrade"`
	AdminMenus   *struct{} `json:"admin_menus"`
	AdminCRUD    *struct{} `json:"admin_crud"`
}

type legacyTrustedPluginCodeRequest struct {
	Code string `json:"code"`
}

type legacyTrustedPluginConfigRequest struct {
	Code   string         `json:"code"`
	Config map[string]any `json:"config"`
}

type legacyTrustedPluginConfigField struct {
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Placeholder string   `json:"placeholder"`
	Description string   `json:"description"`
	Value       any      `json:"value"`
	Options     []string `json:"options"`
}

type legacyTelegramPluginConfigResponse struct {
	EnableTicketNotify  legacyTrustedPluginConfigField `json:"enable_ticket_notify"`
	EnablePaymentNotify legacyTrustedPluginConfigField `json:"enable_payment_notify"`
	StartWelcomeTitle   legacyTrustedPluginConfigField `json:"start_welcome_title"`
	StartBotDescription legacyTrustedPluginConfigField `json:"start_bot_description"`
	StartBindGuide      legacyTrustedPluginConfigField `json:"start_bind_guide"`
	StartUnbindGuide    legacyTrustedPluginConfigField `json:"start_unbind_guide"`
	StartBindCommands   legacyTrustedPluginConfigField `json:"start_bind_commands"`
	StartFooter         legacyTrustedPluginConfigField `json:"start_footer"`
	HelpText            legacyTrustedPluginConfigField `json:"help_text"`
}

func (s *server) legacyTrustedPluginTypes(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "查询参数无效")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{
		{
			"value":       "feature",
			"label":       "功能",
			"description": "提供功能扩展的插件，如Telegram登录、邮件通知等",
			"icon":        "🔧",
		},
		{
			"value":       "payment",
			"label":       "支付方式",
			"description": "提供支付接口的插件，如支付宝、微信支付等",
			"icon":        "💳",
		},
	}})
}

func (s *server) legacyListTrustedPlugins(w http.ResponseWriter, r *http.Request) {
	pluginType, ok := legacyPluginSingleQuery(w, r, "type", false)
	if !ok {
		return
	}
	if pluginType != "" && pluginType != "feature" && pluginType != "payment" {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "插件类型无效")
		return
	}
	plugins, err := s.store.ListTrustedPlugins(r.Context())
	if err != nil {
		writeLegacyPluginFailure(w, http.StatusInternalServerError, "插件列表读取失败")
		return
	}
	sort.Slice(plugins, func(left, right int) bool {
		return legacyTrustedPluginOrder(plugins[left].Code) < legacyTrustedPluginOrder(plugins[right].Code)
	})
	result := make([]legacyTrustedPluginResponse, 0, len(plugins))
	for _, plugin := range plugins {
		if pluginType != "" && plugin.Type != pluginType {
			continue
		}
		name, description, found := legacyTrustedPluginMetadata(plugin.Code)
		if !found {
			writeLegacyPluginFailure(w, http.StatusInternalServerError, "插件目录包含不受信代码")
			return
		}
		var config any = []any{}
		if plugin.Code == store.TrustedPluginTelegram {
			telegramConfig, valid := legacyTelegramPluginConfig(plugin.Config)
			if !valid {
				writeLegacyPluginFailure(w, http.StatusInternalServerError, "Telegram 插件配置损坏")
				return
			}
			config = telegramConfig
		} else if len(plugin.Config) != 0 {
			writeLegacyPluginFailure(w, http.StatusInternalServerError, "支付插件包含不受信配置")
			return
		}
		readme := ""
		if plugin.Code == store.TrustedPluginTelegram {
			readme = legacyTelegramPluginReadme
		}
		result = append(result, legacyTrustedPluginResponse{
			Code:         plugin.Code,
			Name:         name,
			Version:      plugin.Version,
			Description:  description,
			Author:       "XBoard Team",
			Type:         plugin.Type,
			IsInstalled:  true,
			IsEnabled:    plugin.Enabled,
			IsProtected:  true,
			CanBeDeleted: false,
			Config:       config,
			Readme:       readme,
			NeedUpgrade:  false,
			AdminMenus:   nil,
			AdminCRUD:    nil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *server) legacyEnableTrustedPlugin(w http.ResponseWriter, r *http.Request) {
	s.legacySetTrustedPluginEnabled(w, r, true)
}

func (s *server) legacyDisableTrustedPlugin(w http.ResponseWriter, r *http.Request) {
	s.legacySetTrustedPluginEnabled(w, r, false)
}

func (s *server) legacySetTrustedPluginEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var input legacyTrustedPluginCodeRequest
	if !decodeTrustedPluginJSON(w, r, &input) {
		return
	}
	plugin, err := s.store.GetTrustedPlugin(r.Context(), input.Code)
	if err != nil {
		writeLegacyTrustedPluginStoreError(w, err)
		return
	}
	if plugin.Enabled != enabled {
		session, _ := sessionFromContext(r.Context())
		if _, err := s.store.UpdateTrustedPlugin(r.Context(), session.UserID, plugin.Code, plugin.Revision, store.SaveTrustedPluginInput{
			Enabled: enabled,
			Config:  plugin.Config,
		}, s.now()); err != nil {
			writeLegacyTrustedPluginStoreError(w, err)
			return
		}
	}
	message := "插件禁用成功"
	if enabled {
		message = "插件启用成功"
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": message})
}

func (s *server) legacyGetTrustedPluginConfig(w http.ResponseWriter, r *http.Request) {
	code, ok := legacyPluginSingleQuery(w, r, "code", true)
	if !ok {
		return
	}
	if code != store.TrustedPluginTelegram {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "仅支持 Telegram 插件配置")
		return
	}
	plugin, err := s.store.GetTrustedPlugin(r.Context(), code)
	if err != nil {
		writeLegacyTrustedPluginStoreError(w, err)
		return
	}
	config, valid := legacyTelegramPluginConfig(plugin.Config)
	if !valid {
		writeLegacyPluginFailure(w, http.StatusInternalServerError, "Telegram 插件配置损坏")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": config})
}

func (s *server) legacyUpdateTrustedPluginConfig(w http.ResponseWriter, r *http.Request) {
	var input legacyTrustedPluginConfigRequest
	if !decodeTrustedPluginJSON(w, r, &input) {
		return
	}
	if input.Code != store.TrustedPluginTelegram {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "仅支持 Telegram 插件配置")
		return
	}
	plugin, err := s.store.GetTrustedPlugin(r.Context(), input.Code)
	if err != nil {
		writeLegacyTrustedPluginStoreError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.UpdateTrustedPlugin(r.Context(), session.UserID, plugin.Code, plugin.Revision, store.SaveTrustedPluginInput{
		Enabled: plugin.Enabled,
		Config:  input.Config,
	}, s.now()); err != nil {
		writeLegacyTrustedPluginStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "配置更新成功"})
}

func (s *server) rejectLegacyRuntimePluginMutation(w http.ResponseWriter, _ *http.Request) {
	writeLegacyPluginFailure(w, http.StatusConflict, "Xboard-Go 不支持运行时上传、删除、安装、卸载或升级插件；请通过受审查版本发布更新受信插件")
}

func (s *server) auditLegacyAdminPluginMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		action := ""
		for _, candidate := range []string{"upload", "delete", "install", "uninstall", "enable", "disable", "config", "upgrade"} {
			if strings.HasSuffix(r.URL.Path, "/plugin/"+candidate) {
				action = candidate
				break
			}
		}
		if action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/plugin/"+action, recorder.statusCode())
	})
}

func legacyPluginSingleQuery(w http.ResponseWriter, r *http.Request, name string, required bool) (string, bool) {
	query := r.URL.Query()
	if len(query) > 1 {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "查询参数无效")
		return "", false
	}
	values, found := query[name]
	if !found {
		if len(query) != 0 {
			writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "查询参数无效")
			return "", false
		}
		if required {
			writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, name+" 参数不能为空")
			return "", false
		}
		return "", true
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) != values[0] || (required && values[0] == "") {
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, name+" 参数无效")
		return "", false
	}
	return values[0], true
}

func legacyTrustedPluginMetadata(code string) (string, string, bool) {
	switch code {
	case store.TrustedPluginTelegram:
		return "Telegram Bot 集成", "Telegram Bot 消息处理和命令系统", true
	case store.TrustedPluginAlipayF2F:
		return "AlipayF2F", "AlipayF2F payment plugin", true
	case store.TrustedPluginBTCPay:
		return "BTCPay", "BTCPay payment plugin", true
	case store.TrustedPluginCoinPayments:
		return "CoinPayments", "CoinPayments payment plugin", true
	case store.TrustedPluginCoinbase:
		return "Coinbase", "Coinbase payment plugin", true
	case store.TrustedPluginEPay:
		return "EPay", "EPay payment plugin", true
	case store.TrustedPluginMGate:
		return "MGate", "MGate payment plugin", true
	default:
		return "", "", false
	}
}

func legacyTrustedPluginOrder(code string) int {
	switch code {
	case store.TrustedPluginAlipayF2F:
		return 0
	case store.TrustedPluginBTCPay:
		return 1
	case store.TrustedPluginCoinbase:
		return 2
	case store.TrustedPluginCoinPayments:
		return 3
	case store.TrustedPluginEPay:
		return 4
	case store.TrustedPluginMGate:
		return 5
	case store.TrustedPluginTelegram:
		return 6
	default:
		return 7
	}
}

func legacyTelegramPluginConfig(values map[string]any) (legacyTelegramPluginConfigResponse, bool) {
	if len(values) != 9 {
		return legacyTelegramPluginConfigResponse{}, false
	}
	boolean := func(key string) (bool, bool) {
		value, ok := values[key].(bool)
		return value, ok
	}
	text := func(key string) (string, bool) {
		value, ok := values[key].(string)
		return value, ok
	}
	ticketNotify, ticketOK := boolean("enable_ticket_notify")
	paymentNotify, paymentOK := boolean("enable_payment_notify")
	welcomeTitle, welcomeOK := text("start_welcome_title")
	botDescription, descriptionOK := text("start_bot_description")
	bindGuide, bindGuideOK := text("start_bind_guide")
	unbindGuide, unbindGuideOK := text("start_unbind_guide")
	bindCommands, bindCommandsOK := text("start_bind_commands")
	footer, footerOK := text("start_footer")
	helpText, helpOK := text("help_text")
	if !ticketOK || !paymentOK || !welcomeOK || !descriptionOK || !bindGuideOK || !unbindGuideOK || !bindCommandsOK || !footerOK || !helpOK {
		return legacyTelegramPluginConfigResponse{}, false
	}
	field := func(fieldType, label, description string, value any) legacyTrustedPluginConfigField {
		return legacyTrustedPluginConfigField{
			Type: fieldType, Label: label, Placeholder: "", Description: description, Value: value, Options: []string{},
		}
	}
	return legacyTelegramPluginConfigResponse{
		EnableTicketNotify:  field("boolean", "开启工单通知", "是否开启工单创建和回复的 Telegram 通知功能", ticketNotify),
		EnablePaymentNotify: field("boolean", "开启支付通知", "是否开启支付成功的 Telegram 通知功能", paymentNotify),
		StartWelcomeTitle:   field("string", "欢迎标题", "/start 命令显示的欢迎标题", welcomeTitle),
		StartBotDescription: field("text", "机器人描述", "/start 命令显示的机器人功能介绍", botDescription),
		StartBindGuide:      field("text", "绑定指导", "未绑定用户显示的绑定指导文本", bindGuide),
		StartUnbindGuide:    field("text", "已绑定用户命令列表", "已绑定用户显示的命令列表", unbindGuide),
		StartBindCommands:   field("text", "未绑定用户命令列表", "未绑定用户显示的命令列表", bindCommands),
		StartFooter:         field("text", "底部提示", "/start 命令底部的提示信息", footer),
		HelpText:            field("text", "帮助文本", "未知命令时显示的帮助文本", helpText),
	}, true
}

func writeLegacyTrustedPluginStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "插件不存在或不受信")
	case errors.Is(err, store.ErrRevisionConflict):
		writeLegacyPluginFailure(w, http.StatusConflict, "插件已被其他管理员修改，请刷新后重试")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyPluginFailure(w, http.StatusUnprocessableEntity, "插件配置参数无效")
	default:
		writeLegacyPluginFailure(w, http.StatusInternalServerError, "插件操作失败")
	}
}

func writeLegacyPluginFailure(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}
