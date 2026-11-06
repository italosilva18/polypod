// Package config provides GlobalConfig, ProjectConfig, MCP server config,
// and related I/O helpers ported from the TypeScript config.ts source.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// ─── Notification / editor constants ────────────────────────────────────────

type NotificationChannel string

const (
	NotifAuto           NotificationChannel = "auto"
	NotifIterm2         NotificationChannel = "iterm2"
	NotifIterm2WithBell NotificationChannel = "iterm2_with_bell"
	NotifTerminalBell   NotificationChannel = "terminal_bell"
	NotifKitty          NotificationChannel = "kitty"
	NotifGhostty        NotificationChannel = "ghostty"
	NotifDisabled       NotificationChannel = "notifications_disabled"
)

type EditorMode string

const (
	EditorNormal EditorMode = "normal"
	EditorVim    EditorMode = "vim"
	EditorEmacs  EditorMode = "emacs" // deprecated – auto-migrated to normal
)

type DiffTool string

const (
	DiffToolTerminal DiffTool = "terminal"
	DiffToolAuto     DiffTool = "auto"
)

type InstallMethod string

const (
	InstallLocal   InstallMethod = "local"
	InstallNative  InstallMethod = "native"
	InstallGlobal  InstallMethod = "global"
	InstallUnknown InstallMethod = "unknown"
)

type ThemeSetting string

// ─── Sub-structs ─────────────────────────────────────────────────────────────

// AccountInfo mirrors the TS AccountInfo type.
type AccountInfo struct {
	AccountUUID           string  `json:"accountUuid"`
	EmailAddress          string  `json:"emailAddress"`
	OrganizationUUID      string  `json:"organizationUuid,omitempty"`
	OrganizationName      *string `json:"organizationName,omitempty"`
	OrganizationRole      *string `json:"organizationRole,omitempty"`
	WorkspaceRole         *string `json:"workspaceRole,omitempty"`
	DisplayName           string  `json:"displayName,omitempty"`
	HasExtraUsageEnabled  bool    `json:"hasExtraUsageEnabled,omitempty"`
	BillingType           *string `json:"billingType,omitempty"`
	AccountCreatedAt      string  `json:"accountCreatedAt,omitempty"`
	SubscriptionCreatedAt string  `json:"subscriptionCreatedAt,omitempty"`
}

// S1MAccessEntry is a per-org Sonnet-1M access cache entry.
type S1MAccessEntry struct {
	HasAccess             bool  `json:"hasAccess"`
	HasAccessNotAsDefault bool  `json:"hasAccessNotAsDefault,omitempty"`
	Timestamp             int64 `json:"timestamp"`
}

// PassesEligibilityEntry holds referral eligibility + timestamp.
type PassesEligibilityEntry struct {
	Timestamp int64 `json:"timestamp"`
	// Remaining fields are opaque from the API
	Extra map[string]interface{} `json:"-"`
}

// GroveConfigEntry holds per-account Grove config cache.
type GroveConfigEntry struct {
	GroveEnabled bool  `json:"grove_enabled"`
	Timestamp    int64 `json:"timestamp"`
}

// OverageCreditInfo holds overage credit info from the API.
type OverageCreditInfo struct {
	Available        bool    `json:"available"`
	Eligible         bool    `json:"eligible"`
	Granted          bool    `json:"granted"`
	AmountMinorUnits *int64  `json:"amount_minor_units"`
	Currency         *string `json:"currency"`
}

// OverageCreditEntry is a per-org cache entry for overage credit grants.
type OverageCreditEntry struct {
	Info      OverageCreditInfo `json:"info"`
	Timestamp int64             `json:"timestamp"`
}

// MetricsStatusCache holds the cached metrics enabled status.
type MetricsStatusCache struct {
	Enabled   bool  `json:"enabled"`
	Timestamp int64 `json:"timestamp"`
}

// ChromeExtensionState holds Chrome extension pairing state.
type ChromeExtensionState struct {
	PairedDeviceID   string `json:"pairedDeviceId,omitempty"`
	PairedDeviceName string `json:"pairedDeviceName,omitempty"`
}

// ClaudeCodeHints holds hint protocol state.
type ClaudeCodeHints struct {
	Plugin   []string `json:"plugin,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// FeedbackSurveyState tracks feedback survey.
type FeedbackSurveyState struct {
	LastShownTime *int64 `json:"lastShownTime,omitempty"`
}

// CustomAPIKeyResponses tracks approved / rejected custom API keys.
type CustomAPIKeyResponses struct {
	Approved []string `json:"approved,omitempty"`
	Rejected []string `json:"rejected,omitempty"`
}

// SkillUsageEntry tracks skill usage for autocomplete ranking.
type SkillUsageEntry struct {
	UsageCount int   `json:"usageCount"`
	LastUsedAt int64 `json:"lastUsedAt"`
}

// ModelUsageEntry holds per-model token usage.
type ModelUsageEntry struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
}

// MCPServerConfig describes one MCP server in the global config.
type MCPServerConfig struct {
	Type    string            `json:"type"`              // "stdio" or "sse"
	Command string            `json:"command,omitempty"` // for stdio
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`     // for sse
	Headers map[string]string `json:"headers,omitempty"` // for sse
}

// ProjectConfig stores per-project overrides keyed by normalised project root.
type ProjectConfig struct {
	Model      string   `json:"model,omitempty"`
	Agent      string   `json:"agent,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	MCPServers []string `json:"mcpServers,omitempty"`
}

// ─── GlobalConfig ─────────────────────────────────────────────────────────

// GlobalConfig mirrors the TS GlobalConfig type stored in ~/.claude.json.
// All optional fields use pointer types so that zero values are omitted from
// JSON (matching the TS "pickBy defaults" filtering behaviour).
type GlobalConfig struct {
	// Auth
	OAuthAccount  *AccountInfo `json:"oauthAccount,omitempty"`
	PrimaryAPIKey string       `json:"primaryApiKey,omitempty"`

	// Startup / install
	NumStartups    int            `json:"numStartups"`
	InstallMethod  *InstallMethod `json:"installMethod,omitempty"`
	UserID         string         `json:"userID,omitempty"`
	FirstStartTime string         `json:"firstStartTime,omitempty"`

	// Auto-update
	AutoUpdates                   *bool `json:"autoUpdates,omitempty"`
	AutoUpdatesProtectedForNative *bool `json:"autoUpdatesProtectedForNative,omitempty"`

	// Onboarding
	HasCompletedOnboarding *bool  `json:"hasCompletedOnboarding,omitempty"`
	LastOnboardingVersion  string `json:"lastOnboardingVersion,omitempty"`
	LastReleaseNotesSeen   string `json:"lastReleaseNotesSeen,omitempty"`
	ChangelogLastFetched   *int64 `json:"changelogLastFetched,omitempty"`

	// MCP
	MCPServers               map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	ClaudeAiMCPEverConnected []string                   `json:"claudeAiMcpEverConnected,omitempty"`

	// Notification / UI
	PreferredNotifChannel NotificationChannel `json:"preferredNotifChannel"`
	CustomNotifyCommand   string              `json:"customNotifyCommand,omitempty"`
	Verbose               bool                `json:"verbose"`
	Theme                 ThemeSetting        `json:"theme"`
	EditorMode            EditorMode          `json:"editorMode,omitempty"`
	DiffTool              *DiffTool           `json:"diffTool,omitempty"`

	// Compact / display
	AutoCompactEnabled bool `json:"autoCompactEnabled"`
	ShowTurnDuration   bool `json:"showTurnDuration"`

	// Custom API key responses
	CustomAPIKeyResponses *CustomAPIKeyResponses `json:"customApiKeyResponses,omitempty"`

	// Env passthrough
	Env map[string]string `json:"env"`

	// Tips / usage hints
	TipsHistory              map[string]int `json:"tipsHistory"`
	HasSeenTasksHint         *bool          `json:"hasSeenTasksHint,omitempty"`
	HasUsedStash             *bool          `json:"hasUsedStash,omitempty"`
	HasUsedBackgroundTask    *bool          `json:"hasUsedBackgroundTask,omitempty"`
	QueuedCommandUpHintCount *int           `json:"queuedCommandUpHintCount,omitempty"`
	HasUsedBackslashReturn   *bool          `json:"hasUsedBackslashReturn,omitempty"`

	// Memory
	MemoryUsageCount int `json:"memoryUsageCount"`

	// Todo feature
	TodoFeatureEnabled bool  `json:"todoFeatureEnabled"`
	ShowExpandedTodos  *bool `json:"showExpandedTodos,omitempty"`
	ShowSpinnerTree    *bool `json:"showSpinnerTree,omitempty"`

	// Notifications threshold
	MessageIdleNotifThresholdMs int `json:"messageIdleNotifThresholdMs"`

	// IDE
	AutoConnectIDE                   *bool           `json:"autoConnectIde,omitempty"`
	AutoInstallIdeExtension          *bool           `json:"autoInstallIdeExtension,omitempty"`
	HasIdeOnboardingBeenShown        map[string]bool `json:"hasIdeOnboardingBeenShown,omitempty"`
	IdeHintShownCount                *int            `json:"ideHintShownCount,omitempty"`
	HasIdeAutoConnectDialogBeenShown *bool           `json:"hasIdeAutoConnectDialogBeenShown,omitempty"`

	// File checkpointing / progress bar
	FileCheckpointingEnabled   bool  `json:"fileCheckpointingEnabled"`
	TerminalProgressBarEnabled bool  `json:"terminalProgressBarEnabled"`
	ShowStatusInTerminalTab    *bool `json:"showStatusInTerminalTab,omitempty"`

	// Push notifications
	TaskCompleteNotifEnabled *bool `json:"taskCompleteNotifEnabled,omitempty"`
	InputNeededNotifEnabled  *bool `json:"inputNeededNotifEnabled,omitempty"`
	AgentPushNotifEnabled    *bool `json:"agentPushNotifEnabled,omitempty"`

	// Terminal setup
	Iterm2SetupInProgress         *bool  `json:"iterm2SetupInProgress,omitempty"`
	Iterm2BackupPath              string `json:"iterm2BackupPath,omitempty"`
	AppleTerminalBackupPath       string `json:"appleTerminalBackupPath,omitempty"`
	AppleTerminalSetupInProgress  *bool  `json:"appleTerminalSetupInProgress,omitempty"`
	Iterm2KeyBindingInstalled     *bool  `json:"iterm2KeyBindingInstalled,omitempty"`
	ShiftEnterKeyBindingInstalled *bool  `json:"shiftEnterKeyBindingInstalled,omitempty"`
	OptionAsMetaKeyInstalled      *bool  `json:"optionAsMetaKeyInstalled,omitempty"`
	Iterm2It2SetupComplete        *bool  `json:"iterm2It2SetupComplete,omitempty"`
	PreferTmuxOverIterm2          *bool  `json:"preferTmuxOverIterm2,omitempty"`

	// Permissions
	BypassPermissionsModeAccepted *bool `json:"bypassPermissionsModeAccepted,omitempty"`
	PermissionExplainerEnabled    *bool `json:"permissionExplainerEnabled,omitempty"`

	// Respect .gitignore in file picker
	RespectGitignore bool `json:"respectGitignore"`

	// Copy behaviour
	CopyFullResponse bool  `json:"copyFullResponse"`
	CopyOnSelect     *bool `json:"copyOnSelect,omitempty"`

	// Doctor
	DoctorShownAtSession *int `json:"doctorShownAtSession,omitempty"`

	// Statsig / GrowthBook caches
	CachedStatsigGates       map[string]bool        `json:"cachedStatsigGates"`
	CachedDynamicConfigs     map[string]interface{} `json:"cachedDynamicConfigs,omitempty"`
	CachedGrowthBookFeatures map[string]interface{} `json:"cachedGrowthBookFeatures,omitempty"`
	GrowthBookOverrides      map[string]interface{} `json:"growthBookOverrides,omitempty"`

	// Sonnet-1M
	HasShownS1MWelcomeV2        map[string]bool           `json:"hasShownS1MWelcomeV2,omitempty"`
	S1MAccessCache              map[string]S1MAccessEntry `json:"s1mAccessCache,omitempty"`
	S1MNonSubscriberAccessCache map[string]S1MAccessEntry `json:"s1mNonSubscriberAccessCache,omitempty"`

	// Passes / referral
	PassesEligibilityCache  map[string]map[string]interface{} `json:"passesEligibilityCache,omitempty"`
	PassesUpsellSeenCount   *int                              `json:"passesUpsellSeenCount,omitempty"`
	HasVisitedPasses        *bool                             `json:"hasVisitedPasses,omitempty"`
	PassesLastSeenRemaining *int                              `json:"passesLastSeenRemaining,omitempty"`

	// Grove
	GroveConfigCache map[string]GroveConfigEntry `json:"groveConfigCache,omitempty"`

	// Overage credits
	OverageCreditGrantCache      map[string]OverageCreditEntry `json:"overageCreditGrantCache,omitempty"`
	OverageCreditUpsellSeenCount *int                          `json:"overageCreditUpsellSeenCount,omitempty"`
	HasVisitedExtraUsage         *bool                         `json:"hasVisitedExtraUsage,omitempty"`

	// Voice mode
	VoiceNoticeSeenCount      *int   `json:"voiceNoticeSeenCount,omitempty"`
	VoiceLangHintShownCount   *int   `json:"voiceLangHintShownCount,omitempty"`
	VoiceLangHintLastLanguage string `json:"voiceLangHintLastLanguage,omitempty"`
	VoiceFooterHintSeenCount  *int   `json:"voiceFooterHintSeenCount,omitempty"`

	// Opus 1M merge notice
	Opus1MMergeNoticeSeenCount *int `json:"opus1mMergeNoticeSeenCount,omitempty"`

	// Experiment notices
	ExperimentNoticesSeenCount map[string]int `json:"experimentNoticesSeenCount,omitempty"`

	// OpusPlan
	HasShownOpusPlanWelcome map[string]bool `json:"hasShownOpusPlanWelcome,omitempty"`

	// Queue / btw usage
	PromptQueueUseCount int    `json:"promptQueueUseCount"`
	BtwUseCount         int    `json:"btwUseCount"`
	LastPlanModeUse     *int64 `json:"lastPlanModeUse,omitempty"`

	// Subscription
	SubscriptionNoticeCount      *int   `json:"subscriptionNoticeCount,omitempty"`
	HasAvailableSubscription     *bool  `json:"hasAvailableSubscription,omitempty"`
	SubscriptionUpsellShownCount *int   `json:"subscriptionUpsellShownCount,omitempty"`
	RecommendedSubscription      string `json:"recommendedSubscription,omitempty"`

	// Feedback survey
	FeedbackSurveyState *FeedbackSurveyState `json:"feedbackSurveyState,omitempty"`

	// Transcript share
	TranscriptShareDismissed *bool `json:"transcriptShareDismissed,omitempty"`

	// Companion
	CompanionMuted *bool `json:"companionMuted,omitempty"`

	// GitHub / Slack integrations
	GithubActionSetupCount *int `json:"githubActionSetupCount,omitempty"`
	SlackAppInstallCount   *int `json:"slackAppInstallCount,omitempty"`

	// GitHub repo paths
	GithubRepoPaths map[string][]string `json:"githubRepoPaths,omitempty"`

	// Deep link terminal
	DeepLinkTerminal string `json:"deepLinkTerminal,omitempty"`

	// Model switch callout
	ModelSwitchCalloutDismissed *bool  `json:"modelSwitchCalloutDismissed,omitempty"`
	ModelSwitchCalloutLastShown *int64 `json:"modelSwitchCalloutLastShown,omitempty"`
	ModelSwitchCalloutVersion   string `json:"modelSwitchCalloutVersion,omitempty"`

	// Effort callout
	EffortCalloutDismissed   *bool `json:"effortCalloutDismissed,omitempty"`
	EffortCalloutV2Dismissed *bool `json:"effortCalloutV2Dismissed,omitempty"`

	// Remote control
	RemoteDialogSeen       *bool `json:"remoteDialogSeen,omitempty"`
	RemoteControlAtStartup *bool `json:"remoteControlAtStartup,omitempty"`

	// Bridge oauth dead state
	BridgeOauthDeadExpiresAt *int64 `json:"bridgeOauthDeadExpiresAt,omitempty"`
	BridgeOauthDeadFailCount *int   `json:"bridgeOauthDeadFailCount,omitempty"`

	// Desktop upsell
	DesktopUpsellSeenCount *int  `json:"desktopUpsellSeenCount,omitempty"`
	DesktopUpsellDismissed *bool `json:"desktopUpsellDismissed,omitempty"`

	// Idle-return dialog
	IdleReturnDismissed *bool `json:"idleReturnDismissed,omitempty"`

	// Opus/Sonnet migrations
	OpusProMigrationComplete       *bool  `json:"opusProMigrationComplete,omitempty"`
	OpusProMigrationTimestamp      *int64 `json:"opusProMigrationTimestamp,omitempty"`
	Sonnet1m45MigrationComplete    *bool  `json:"sonnet1m45MigrationComplete,omitempty"`
	LegacyOpusMigrationTimestamp   *int64 `json:"legacyOpusMigrationTimestamp,omitempty"`
	Sonnet45To46MigrationTimestamp *int64 `json:"sonnet45To46MigrationTimestamp,omitempty"`

	// Emergency tip
	LastShownEmergencyTip string `json:"lastShownEmergencyTip,omitempty"`

	// Claude Code first token
	ClaudeCodeFirstTokenDate string `json:"claudeCodeFirstTokenDate,omitempty"`

	// Skill usage
	SkillUsage map[string]SkillUsageEntry `json:"skillUsage,omitempty"`

	// Official marketplace auto-install
	OfficialMarketplaceAutoInstallAttempted       *bool  `json:"officialMarketplaceAutoInstallAttempted,omitempty"`
	OfficialMarketplaceAutoInstalled              *bool  `json:"officialMarketplaceAutoInstalled,omitempty"`
	OfficialMarketplaceAutoInstallFailReason      string `json:"officialMarketplaceAutoInstallFailReason,omitempty"`
	OfficialMarketplaceAutoInstallRetryCount      *int   `json:"officialMarketplaceAutoInstallRetryCount,omitempty"`
	OfficialMarketplaceAutoInstallLastAttemptTime *int64 `json:"officialMarketplaceAutoInstallLastAttemptTime,omitempty"`
	OfficialMarketplaceAutoInstallNextRetryTime   *int64 `json:"officialMarketplaceAutoInstallNextRetryTime,omitempty"`

	// Claude in Chrome
	HasCompletedClaudeInChromeOnboarding *bool                 `json:"hasCompletedClaudeInChromeOnboarding,omitempty"`
	ClaudeInChromeDefaultEnabled         *bool                 `json:"claudeInChromeDefaultEnabled,omitempty"`
	CachedChromeExtensionInstalled       *bool                 `json:"cachedChromeExtensionInstalled,omitempty"`
	ChromeExtension                      *ChromeExtensionState `json:"chromeExtension,omitempty"`

	// LSP recommendation
	LspRecommendationDisabled     *bool    `json:"lspRecommendationDisabled,omitempty"`
	LspRecommendationNeverPlugins []string `json:"lspRecommendationNeverPlugins,omitempty"`
	LspRecommendationIgnoredCount *int     `json:"lspRecommendationIgnoredCount,omitempty"`

	// Hint protocol
	ClaudeCodeHints *ClaudeCodeHints `json:"claudeCodeHints,omitempty"`

	// Teammate
	TeammateMode         string  `json:"teammateMode,omitempty"`
	TeammateDefaultModel *string `json:"teammateDefaultModel,omitempty"`

	// PR status footer
	PrStatusFooterEnabled *bool `json:"prStatusFooterEnabled,omitempty"`

	// Tmux panel
	TungstenPanelVisible *bool `json:"tungstenPanelVisible,omitempty"`

	// Penguin / fast mode
	PenguinModeOrgEnabled *bool `json:"penguinModeOrgEnabled,omitempty"`

	// Startup prefetch timestamp
	StartupPrefetchedAt *int64 `json:"startupPrefetchedAt,omitempty"`

	// Extra usage disabled reason (null = enabled, string = reason)
	CachedExtraUsageDisabledReason *string `json:"cachedExtraUsageDisabledReason,omitempty"`

	// Auto permissions notification
	AutoPermissionsNotificationCount *int `json:"autoPermissionsNotificationCount,omitempty"`

	// Speculation
	SpeculationEnabled *bool `json:"speculationEnabled,omitempty"`

	// Client data / model options caches
	ClientDataCache             map[string]interface{}   `json:"clientDataCache,omitempty"`
	AdditionalModelOptionsCache []map[string]interface{} `json:"additionalModelOptionsCache,omitempty"`

	// Metrics status cache
	MetricsStatusCache *MetricsStatusCache `json:"metricsStatusCache,omitempty"`

	// Migration version
	MigrationVersion *int `json:"migrationVersion,omitempty"`

	// API key helper (deprecated)
	APIKeyHelper string `json:"apiKeyHelper,omitempty"`

	// Projects map — keyed by normalised project root path
	Projects map[string]ProjectConfig `json:"projects,omitempty"`

	// Has seen various notices
	HasSeenUndercoverAutoNotice          *bool `json:"hasSeenUndercoverAutoNotice,omitempty"`
	HasSeenUltraplanTerms                *bool `json:"hasSeenUltraplanTerms,omitempty"`
	HasResetAutoModeOptInForDefaultOffer *bool `json:"hasResetAutoModeOptInForDefaultOffer,omitempty"`

	// Model settings (stored per-session in old TS; here stored as global pref)
	ModelSwitchCalloutLastChecked *int64 `json:"lastCheckedForUpdates,omitempty"`
}

// ─── Defaults ─────────────────────────────────────────────────────────────

// DefaultGlobalConfig returns a fresh GlobalConfig with the same defaults as
// the TS createDefaultGlobalConfig() factory.
func DefaultGlobalConfig() *GlobalConfig {
	theme := ThemeSetting("dark")
	notif := NotifAuto
	editor := EditorNormal
	diffTool := DiffToolAuto
	autoUpdates := true
	autoInstallIde := true
	autoConnectIde := false
	showTurnDuration := true
	seenTasksHint := false
	usedStash := false
	usedBackgroundTask := false
	queueCount := 0
	memUsage := 0
	promptQueueCount := 0
	btwCount := 0
	todoEnabled := true
	showExpTodos := false
	msgIdleMs := 60000
	fileCheckpointing := true
	terminalProgressBar := true
	respectGitignore := true
	copyFull := false

	return &GlobalConfig{
		NumStartups:                 0,
		Theme:                       theme,
		PreferredNotifChannel:       notif,
		Verbose:                     false,
		EditorMode:                  editor,
		DiffTool:                    &diffTool,
		AutoUpdates:                 &autoUpdates,
		AutoCompactEnabled:          true,
		ShowTurnDuration:            showTurnDuration,
		HasSeenTasksHint:            &seenTasksHint,
		HasUsedStash:                &usedStash,
		HasUsedBackgroundTask:       &usedBackgroundTask,
		QueuedCommandUpHintCount:    &queueCount,
		CustomAPIKeyResponses:       &CustomAPIKeyResponses{Approved: []string{}, Rejected: []string{}},
		Env:                         map[string]string{},
		TipsHistory:                 map[string]int{},
		MemoryUsageCount:            memUsage,
		PromptQueueUseCount:         promptQueueCount,
		BtwUseCount:                 btwCount,
		TodoFeatureEnabled:          todoEnabled,
		ShowExpandedTodos:           &showExpTodos,
		MessageIdleNotifThresholdMs: msgIdleMs,
		AutoConnectIDE:              &autoConnectIde,
		AutoInstallIdeExtension:     &autoInstallIde,
		FileCheckpointingEnabled:    fileCheckpointing,
		TerminalProgressBarEnabled:  terminalProgressBar,
		CachedStatsigGates:          map[string]bool{},
		CachedDynamicConfigs:        map[string]interface{}{},
		CachedGrowthBookFeatures:    map[string]interface{}{},
		RespectGitignore:            respectGitignore,
		CopyFullResponse:            copyFull,
	}
}

// ─── Cache ────────────────────────────────────────────────────────────────

var (
	globalConfigMu    sync.RWMutex
	globalConfigCache *GlobalConfig
	globalConfigMtime int64 // unix nano of the last read/write
)

// ResetGlobalConfigCache invalidates the in-memory cache, forcing the next
// call to GetGlobalConfig to re-read from disk.
func ResetGlobalConfigCache() {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()
	globalConfigCache = nil
	globalConfigMtime = 0
}

// ─── Read ─────────────────────────────────────────────────────────────────

// GetGlobalConfig returns the global config, using a memoised in-memory copy.
// The freshness watcher (started on first load) updates the cache when the
// file is modified by another process.
func GetGlobalConfig() (*GlobalConfig, error) {
	globalConfigMu.RLock()
	cached := globalConfigCache
	globalConfigMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	return loadAndCacheGlobalConfig()
}

func loadAndCacheGlobalConfig() (*GlobalConfig, error) {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	// Double-check after acquiring write lock.
	if globalConfigCache != nil {
		return globalConfigCache, nil
	}

	path := globalConfigFilePath()
	cfg, mtime, err := readGlobalConfigFromDisk(path)
	if err != nil {
		return nil, err
	}

	globalConfigCache = cfg
	globalConfigMtime = mtime
	return cfg, nil
}

// readGlobalConfigFromDisk reads and parses the JSON config file, falling
// back to defaults when the file is absent.  Returns the parsed config and
// the file modification time (unix nano).
func readGlobalConfigFromDisk(path string) (*GlobalConfig, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultGlobalConfig(), time.Now().UnixNano(), nil
		}
		return nil, 0, fmt.Errorf("reading global config: %w", err)
	}

	st, statErr := os.Stat(path)
	var mtime int64
	if statErr == nil {
		mtime = st.ModTime().UnixNano()
	} else {
		mtime = time.Now().UnixNano()
	}

	cfg := DefaultGlobalConfig()
	if jsonErr := json.Unmarshal(stripBOMBytes(data), cfg); jsonErr != nil {
		// Corrupted — save a corrupted backup and return defaults.
		_ = backupCorrupted(path)
		return DefaultGlobalConfig(), mtime, nil
	}

	migrateGlobalConfigFields(cfg)
	return cfg, mtime, nil
}

// ─── Write ────────────────────────────────────────────────────────────────

// SaveGlobalConfig atomically persists cfg to disk under an advisory file
// lock, creating a timestamped backup first (keeping at most maxBackups).
func SaveGlobalConfig(cfg *GlobalConfig) error {
	path := globalConfigFilePath()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	// Acquire advisory lock.
	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer lf.Close() //nolint:errcheck

	if flockErr := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); flockErr != nil {
		return fmt.Errorf("acquiring config lock: %w", flockErr)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	// Auth-loss guard: if the on-disk copy would lose oauth state held in the
	// cache, refuse the write.
	if wouldLoseAuthState(cfg) {
		return fmt.Errorf("refusing to write config: would lose auth state")
	}

	// Backup rotation before write.
	if rotatErr := rotateBackups(path, 3); rotatErr != nil {
		// Non-fatal: log and continue.
		_ = rotatErr
	}

	// Marshal to JSON.
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling global config: %w", err)
	}

	// Atomic write via temp file + rename.
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming config file: %w", err)
	}

	// Write-through cache.
	globalConfigMu.Lock()
	globalConfigCache = cfg
	globalConfigMtime = time.Now().UnixNano()
	globalConfigMu.Unlock()

	return nil
}

// wouldLoseAuthState returns true if writing next would discard auth data
// that the in-memory cache still holds (mirrors the TS guard).
func wouldLoseAuthState(next *GlobalConfig) bool {
	globalConfigMu.RLock()
	cached := globalConfigCache
	globalConfigMu.RUnlock()
	if cached == nil {
		return false
	}
	lostOAuth := cached.OAuthAccount != nil && next.OAuthAccount == nil
	lostOnboarding := (cached.HasCompletedOnboarding != nil && *cached.HasCompletedOnboarding) &&
		(next.HasCompletedOnboarding == nil || !*next.HasCompletedOnboarding)
	return lostOAuth || lostOnboarding
}

// ─── Backup rotation ──────────────────────────────────────────────────────

const minBackupIntervalMs = 60_000

// rotateBackups creates a timestamped backup of path (if one doesn't already
// exist within minBackupIntervalMs), then deletes all but the newest max.
func rotateBackups(path string, max int) error {
	backupDir := configBackupDir()
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}

	base := filepath.Base(path)
	existing, _ := globBackups(backupDir, base)

	// Skip if a recent backup already exists.
	if len(existing) > 0 {
		newest := existing[len(existing)-1]
		ts := backupTimestamp(newest)
		if ts > 0 && time.Now().UnixMilli()-ts < minBackupIntervalMs {
			pruneBackups(backupDir, existing, max)
			return nil
		}
	}

	// Copy current file as backup.
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to back up
		}
		return err
	}
	backupName := fmt.Sprintf("%s.backup.%d", base, time.Now().UnixMilli())
	if err := os.WriteFile(filepath.Join(backupDir, backupName), src, 0600); err != nil {
		return err
	}

	// Re-list and prune.
	all, _ := globBackups(backupDir, base)
	pruneBackups(backupDir, all, max)
	return nil
}

func globBackups(dir, base string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := base + ".backup."
	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // lexicographic = chronological (timestamp suffix)
	return names, nil
}

func pruneBackups(dir string, names []string, max int) {
	for len(names) > max {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func backupTimestamp(name string) int64 {
	// name format: <base>.backup.<ms>
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			var ts int64
			_, err := fmt.Sscanf(name[i+1:], "%d", &ts)
			if err == nil {
				return ts
			}
			break
		}
	}
	return 0
}

func backupCorrupted(path string) error {
	backupDir := configBackupDir()
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}
	base := filepath.Base(path)
	dst := filepath.Join(backupDir, fmt.Sprintf("%s.corrupted.%d", base, time.Now().UnixMilli()))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// ─── File watcher (polling) ───────────────────────────────────────────────

const configFreshnessPollMs = 1000

// WatchGlobalConfig polls the config file every second and calls onChange
// whenever it has been modified by another process.  It returns when ctx is
// cancelled.
func WatchGlobalConfig(ctx context.Context, onChange func(*GlobalConfig)) error {
	path := globalConfigFilePath()
	ticker := time.NewTicker(configFreshnessPollMs * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			diskMtime := st.ModTime().UnixNano()

			globalConfigMu.RLock()
			cachedMtime := globalConfigMtime
			globalConfigMu.RUnlock()

			if diskMtime <= cachedMtime {
				continue // no change, or our own write
			}

			cfg, mtime, err := readGlobalConfigFromDisk(path)
			if err != nil {
				continue
			}

			globalConfigMu.Lock()
			// Another goroutine may have updated while we read; only advance.
			if mtime > globalConfigMtime {
				globalConfigCache = cfg
				globalConfigMtime = mtime
				globalConfigMu.Unlock()
				onChange(cfg)
			} else {
				globalConfigMu.Unlock()
			}
		}
	}
}

// ─── Paths ────────────────────────────────────────────────────────────────

// globalConfigFilePath returns ~/.claude.json (mirrors TS getGlobalClaudeFile).
func globalConfigFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude.json")
}

func configBackupDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "backups")
}

// ─── Migrations ─────────────────────────────────────────────────────────

// migrateGlobalConfigFields applies forward-compatible field migrations.
func migrateGlobalConfigFields(cfg *GlobalConfig) {
	// Initialize maps if nil
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]MCPServerConfig)
	}
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]ProjectConfig)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────

// stripBOMBytes removes a leading UTF-8 BOM if present (PowerShell compat).
func stripBOMBytes(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}
