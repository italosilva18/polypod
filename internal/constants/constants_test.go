package constants

import (
	"strings"
	"testing"
)

// TestProductIdentityNonEmpty verifies all core product identity constants are populated.
func TestProductIdentityNonEmpty(t *testing.T) {
	cases := map[string]string{
		"ProductName":               ProductName,
		"ProductNameCLI":            ProductNameCLI,
		"DefaultCLISyspromptPrefix": DefaultCLISyspromptPrefix,
		"AgentSDKPrefix":            AgentSDKPrefix,
		"ConfigFileName":            ConfigFileName,
		"ConfigLocalFileName":       ConfigLocalFileName,
		"ConfigDirName":             ConfigDirName,
		"SettingsFileName":          SettingsFileName,
		"SkillsDirName":             SkillsDirName,
		"CommandsDirName":           CommandsDirName,
		"AgentsDirName":             AgentsDirName,
		"SkillFileName":             SkillFileName,
		"DefaultOutputStyleName":    DefaultOutputStyleName,
	}
	for name, val := range cases {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}
}

// TestAPIConstantsNonZero ensures numeric API limits have sensible non-zero defaults.
func TestAPIConstantsNonZero(t *testing.T) {
	cases := map[string]int{
		"APIImageMaxBase64Size":       APIImageMaxBase64Size,
		"ImageTargetRawSize":          ImageTargetRawSize,
		"ImageMaxWidth":               ImageMaxWidth,
		"ImageMaxHeight":              ImageMaxHeight,
		"PDFTargetRawSize":            PDFTargetRawSize,
		"APIPDFMaxPages":              APIPDFMaxPages,
		"PDFExtractSizeThreshold":     PDFExtractSizeThreshold,
		"PDFMaxExtractSize":           PDFMaxExtractSize,
		"PDFMaxPagesPerRead":          PDFMaxPagesPerRead,
		"PDFAtMentionInlineThreshold": PDFAtMentionInlineThreshold,
		"APIMaxMediaPerRequest":       APIMaxMediaPerRequest,
	}
	for name, val := range cases {
		if val <= 0 {
			t.Errorf("%s should be > 0, got %d", name, val)
		}
	}
}

// TestImageRawSizeRelationship ensures the raw image cap stays under the
// base64 limit (raw * 4/3 must not exceed APIImageMaxBase64Size).
func TestImageRawSizeRelationship(t *testing.T) {
	if ImageTargetRawSize >= APIImageMaxBase64Size {
		t.Errorf("ImageTargetRawSize (%d) should be < APIImageMaxBase64Size (%d)",
			ImageTargetRawSize, APIImageMaxBase64Size)
	}
	// Approx: raw * 4 / 3 should be <= base64 limit.
	if ImageTargetRawSize*4/3 > APIImageMaxBase64Size {
		t.Errorf("ImageTargetRawSize (%d) base64-encoded would exceed APIImageMaxBase64Size (%d)",
			ImageTargetRawSize, APIImageMaxBase64Size)
	}
}

// TestPDFThresholds ensures PDF thresholds form a valid ordering.
func TestPDFThresholds(t *testing.T) {
	if PDFExtractSizeThreshold >= PDFMaxExtractSize {
		t.Errorf("PDFExtractSizeThreshold (%d) should be < PDFMaxExtractSize (%d)",
			PDFExtractSizeThreshold, PDFMaxExtractSize)
	}
	if PDFMaxPagesPerRead > APIPDFMaxPages {
		t.Errorf("PDFMaxPagesPerRead (%d) should be <= APIPDFMaxPages (%d)",
			PDFMaxPagesPerRead, APIPDFMaxPages)
	}
	if PDFAtMentionInlineThreshold > APIPDFMaxPages {
		t.Errorf("PDFAtMentionInlineThreshold (%d) should be <= APIPDFMaxPages (%d)",
			PDFAtMentionInlineThreshold, APIPDFMaxPages)
	}
}

// TestBetaHeadersNonEmpty ensures all beta header constants are non-empty strings.
func TestBetaHeadersNonEmpty(t *testing.T) {
	betas := map[string]string{
		"ClaudeCode20250219Beta":  ClaudeCode20250219Beta,
		"InterleavedThinkingBeta": InterleavedThinkingBeta,
		"Context1MBeta":           Context1MBeta,
		"ContextManagementBeta":   ContextManagementBeta,
		"StructuredOutputsBeta":   StructuredOutputsBeta,
		"WebSearchBeta":           WebSearchBeta,
		"ToolSearchBeta1P":        ToolSearchBeta1P,
		"ToolSearchBeta3P":        ToolSearchBeta3P,
		"EffortBeta":              EffortBeta,
		"TaskBudgetsBeta":         TaskBudgetsBeta,
		"PromptCachingScopeBeta":  PromptCachingScopeBeta,
		"FastModeBeta":            FastModeBeta,
		"RedactThinkingBeta":      RedactThinkingBeta,
		"TokenEfficientToolsBeta": TokenEfficientToolsBeta,
		"AdvisorBeta":             AdvisorBeta,
		"OAuthBeta":               OAuthBeta,
	}
	for name, val := range betas {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}
}

// TestBedrockExtraParamsHeadersReferenceValidBetas ensures keys in
// BedrockExtraParamsHeaders correspond to known beta header values.
func TestBedrockExtraParamsHeadersReferenceValidBetas(t *testing.T) {
	knownBetas := map[string]bool{
		ClaudeCode20250219Beta:  true,
		InterleavedThinkingBeta: true,
		Context1MBeta:           true,
		ContextManagementBeta:   true,
		StructuredOutputsBeta:   true,
		WebSearchBeta:           true,
		ToolSearchBeta1P:        true,
		ToolSearchBeta3P:        true,
		EffortBeta:              true,
		TaskBudgetsBeta:         true,
		PromptCachingScopeBeta:  true,
		FastModeBeta:            true,
		RedactThinkingBeta:      true,
		TokenEfficientToolsBeta: true,
		AdvisorBeta:             true,
		OAuthBeta:               true,
	}
	for k := range BedrockExtraParamsHeaders {
		if !knownBetas[k] {
			t.Errorf("BedrockExtraParamsHeaders contains unknown beta key %q", k)
		}
	}
	if len(BedrockExtraParamsHeaders) == 0 {
		t.Error("BedrockExtraParamsHeaders should not be empty")
	}
	for k := range VertexCountTokensAllowedBetas {
		if !knownBetas[k] {
			t.Errorf("VertexCountTokensAllowedBetas contains unknown beta key %q", k)
		}
	}
}

// TestRateLimitTiersOrdering verifies tiers have non-empty names and sane values.
func TestRateLimitTiersOrdering(t *testing.T) {
	if len(RateLimitTiers) == 0 {
		t.Fatal("RateLimitTiers should not be empty")
	}
	seenNames := map[string]bool{}
	for i, tier := range RateLimitTiers {
		if tier.Name == "" {
			t.Errorf("tier %d has empty Name", i)
		}
		if seenNames[tier.Name] {
			t.Errorf("duplicate tier name %q", tier.Name)
		}
		seenNames[tier.Name] = true
		if tier.RequestsPerMinute <= 0 {
			t.Errorf("tier %q has non-positive RequestsPerMinute %d", tier.Name, tier.RequestsPerMinute)
		}
		if tier.TokensPerMinute <= 0 {
			t.Errorf("tier %q has non-positive TokensPerMinute %d", tier.Name, tier.TokensPerMinute)
		}
		if tier.TokensPerDay <= 0 {
			t.Errorf("tier %q has non-positive TokensPerDay %d", tier.Name, tier.TokensPerDay)
		}
		if tier.TokensPerDay < tier.TokensPerMinute {
			t.Errorf("tier %q TokensPerDay (%d) should be >= TokensPerMinute (%d)",
				tier.Name, tier.TokensPerDay, tier.TokensPerMinute)
		}
	}
}

// TestModelContextWindow ensures every entry has a positive context window.
func TestModelContextWindow(t *testing.T) {
	if len(ModelContextWindow) == 0 {
		t.Fatal("ModelContextWindow should not be empty")
	}
	for model, window := range ModelContextWindow {
		if model == "" {
			t.Error("ModelContextWindow has empty model key")
		}
		if window <= 0 {
			t.Errorf("model %q has non-positive context window %d", model, window)
		}
	}
	// Frontier model IDs should appear in ModelContextWindow.
	if _, ok := ModelContextWindow[ModelOpus46]; !ok {
		t.Errorf("ModelOpus46 (%s) not present in ModelContextWindow", ModelOpus46)
	}
	if _, ok := ModelContextWindow[ModelSonnet46]; !ok {
		t.Errorf("ModelSonnet46 (%s) not present in ModelContextWindow", ModelSonnet46)
	}
}

// TestModelKnowledgeCutoff verifies the helper returns non-empty cutoffs for
// recognized model families and empty for unknown.
func TestModelKnowledgeCutoff(t *testing.T) {
	cases := []struct {
		model    string
		expectOK bool
	}{
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"claude-opus-4-5", true},
		{"claude-sonnet-4", true},
		{"claude-opus-4", true},
		{"completely-unknown-model", false},
		{"", false},
	}
	for _, c := range cases {
		got := ModelKnowledgeCutoff(c.model)
		if c.expectOK && got == "" {
			t.Errorf("ModelKnowledgeCutoff(%q) returned empty, expected a cutoff", c.model)
		}
		if !c.expectOK && got != "" {
			t.Errorf("ModelKnowledgeCutoff(%q) = %q, expected empty", c.model, got)
		}
	}
}

// TestProductURLs ensures every product URL has a valid scheme.
func TestProductURLs(t *testing.T) {
	urls := map[string]string{
		"ProductURL":             ProductURL,
		"ClaudeAIBaseURL":        ClaudeAIBaseURL,
		"ClaudeAIStagingBaseURL": ClaudeAIStagingBaseURL,
		"ClaudeAILocalBaseURL":   ClaudeAILocalBaseURL,
		"APIBaseURL":             APIBaseURL,
		"ClaudeCodeDocsMapURL":   ClaudeCodeDocsMapURL,
		"MCPClientMetadataURL":   MCPClientMetadataURL,
	}
	for name, u := range urls {
		if u == "" {
			t.Errorf("%s should be non-empty", name)
			continue
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			t.Errorf("%s should start with http(s)://, got %q", name, u)
		}
	}
	if AnthropicAPIVersion == "" {
		t.Error("AnthropicAPIVersion should be non-empty")
	}
}

// TestToolNamesNonEmptyUnique verifies all tool name constants are non-empty and unique.
func TestToolNamesNonEmptyUnique(t *testing.T) {
	tools := map[string]string{
		"ToolReadFile":      ToolReadFile,
		"ToolWriteFile":     ToolWriteFile,
		"ToolEditFile":      ToolEditFile,
		"ToolGlobTool":      ToolGlobTool,
		"ToolGrepTool":      ToolGrepTool,
		"ToolRunCommand":    ToolRunCommand,
		"ToolBash":          ToolBash,
		"ToolPowerShell":    ToolPowerShell,
		"ToolSearchFiles":   ToolSearchFiles,
		"ToolWebSearch":     ToolWebSearch,
		"ToolWebFetch":      ToolWebFetch,
		"ToolMemoryAdd":     ToolMemoryAdd,
		"ToolMemorySearch":  ToolMemorySearch,
		"ToolListSessions":  ToolListSessions,
		"ToolSessionInfo":   ToolSessionInfo,
		"ToolExportSession": ToolExportSession,
		"ToolAgentRun":      ToolAgentRun,
		"ToolSendMessage":   ToolSendMessage,
		"ToolTaskCreate":    ToolTaskCreate,
		"ToolTaskGet":       ToolTaskGet,
		"ToolTaskList":      ToolTaskList,
		"ToolTaskStop":      ToolTaskStop,
		"ToolTaskOutput":    ToolTaskOutput,
		"ToolMCPTool":       ToolMCPTool,
		"ToolTodoWrite":     ToolTodoWrite,
		"ToolTodoRead":      ToolTodoRead,
		"ToolPlanMode":      ToolPlanMode,
		"ToolSkillSearch":   ToolSkillSearch,
		"ToolConfig":        ToolConfig,
	}
	seen := map[string]string{}
	for name, val := range tools {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
			continue
		}
		if other, ok := seen[val]; ok {
			t.Errorf("duplicate tool name value %q for %s and %s", val, name, other)
		}
		seen[val] = name
	}
}

// TestIsReadOnlyTool exercises the IsReadOnlyTool helper.
func TestIsReadOnlyTool(t *testing.T) {
	readOnly := []string{
		ToolReadFile, ToolGlobTool, ToolGrepTool, ToolSearchFiles,
		ToolWebSearch, ToolWebFetch, ToolMemorySearch,
		ToolListSessions, ToolSessionInfo,
	}
	for _, tool := range readOnly {
		if !IsReadOnlyTool(tool) {
			t.Errorf("IsReadOnlyTool(%q) = false, want true", tool)
		}
	}

	notReadOnly := []string{
		ToolWriteFile, ToolEditFile, ToolBash, ToolRunCommand,
		ToolPowerShell, ToolSendMessage, "nonexistent_tool", "",
	}
	for _, tool := range notReadOnly {
		if IsReadOnlyTool(tool) {
			t.Errorf("IsReadOnlyTool(%q) = true, want false", tool)
		}
	}
}

// TestIsDestructiveTool exercises the IsDestructiveTool helper.
func TestIsDestructiveTool(t *testing.T) {
	destructive := []string{
		ToolWriteFile, ToolEditFile, ToolRunCommand, ToolBash,
		ToolPowerShell, ToolSendMessage,
	}
	for _, tool := range destructive {
		if !IsDestructiveTool(tool) {
			t.Errorf("IsDestructiveTool(%q) = false, want true", tool)
		}
	}

	notDestructive := []string{
		ToolReadFile, ToolGlobTool, ToolGrepTool, ToolWebSearch,
		ToolWebFetch, "nonexistent_tool", "",
	}
	for _, tool := range notDestructive {
		if IsDestructiveTool(tool) {
			t.Errorf("IsDestructiveTool(%q) = true, want false", tool)
		}
	}
}

// TestIsConcurrencySafeTool exercises the IsConcurrencySafeTool helper.
func TestIsConcurrencySafeTool(t *testing.T) {
	for _, tool := range ConcurrencySafeTools {
		if !IsConcurrencySafeTool(tool) {
			t.Errorf("IsConcurrencySafeTool(%q) = false, want true", tool)
		}
	}
	if IsConcurrencySafeTool(ToolWriteFile) {
		t.Errorf("IsConcurrencySafeTool(ToolWriteFile) = true, want false")
	}
	if IsConcurrencySafeTool("nonexistent") {
		t.Errorf("IsConcurrencySafeTool(\"nonexistent\") = true, want false")
	}
}

// TestToolPermissionGroupsDisjoint verifies that read-only and destructive
// tool sets do not overlap (cross-reference consistency).
func TestToolPermissionGroupsDisjoint(t *testing.T) {
	readOnlySet := map[string]bool{}
	for _, t := range ReadOnlyTools {
		readOnlySet[t] = true
	}
	for _, tool := range DestructiveTools {
		if readOnlySet[tool] {
			t.Errorf("tool %q appears in both ReadOnlyTools and DestructiveTools", tool)
		}
	}

	// Concurrency-safe tools should all be read-only (a destructive op
	// cannot be safely run concurrently with itself).
	for _, tool := range ConcurrencySafeTools {
		if !readOnlySet[tool] {
			t.Errorf("concurrency-safe tool %q should also be in ReadOnlyTools", tool)
		}
	}
}

// TestToolPermissionGroupsNotEmpty ensures all permission groups have entries
// and reference only known tool name constants.
func TestToolPermissionGroupsNotEmpty(t *testing.T) {
	knownTools := map[string]bool{
		ToolReadFile: true, ToolWriteFile: true, ToolEditFile: true,
		ToolGlobTool: true, ToolGrepTool: true, ToolRunCommand: true,
		ToolBash: true, ToolPowerShell: true, ToolSearchFiles: true,
		ToolWebSearch: true, ToolWebFetch: true, ToolMemoryAdd: true,
		ToolMemorySearch: true, ToolListSessions: true, ToolSessionInfo: true,
		ToolExportSession: true, ToolAgentRun: true, ToolSendMessage: true,
		ToolTaskCreate: true, ToolTaskGet: true, ToolTaskList: true,
		ToolTaskStop: true, ToolTaskOutput: true, ToolMCPTool: true,
		ToolTodoWrite: true, ToolTodoRead: true, ToolPlanMode: true,
		ToolSkillSearch: true, ToolConfig: true,
	}
	groups := map[string][]string{
		"ReadOnlyTools":        ReadOnlyTools,
		"DestructiveTools":     DestructiveTools,
		"ConcurrencySafeTools": ConcurrencySafeTools,
	}
	for name, group := range groups {
		if len(group) == 0 {
			t.Errorf("%s should not be empty", name)
		}
		for _, tool := range group {
			if !knownTools[tool] {
				t.Errorf("%s references unknown tool %q", name, tool)
			}
		}
	}
}

// TestToolLimitsPositive ensures every tool limit constant is positive.
func TestToolLimitsPositive(t *testing.T) {
	cases := map[string]int{
		"MaxOutputBytes":     MaxOutputBytes,
		"MaxFileReadBytes":   MaxFileReadBytes,
		"ExecTimeoutSecs":    ExecTimeoutSecs,
		"MaxToolIterations":  MaxToolIterations,
		"MaxGlobResults":     MaxGlobResults,
		"MaxGrepResults":     MaxGrepResults,
		"MaxConcurrentTools": MaxConcurrentTools,
	}
	for name, val := range cases {
		if val <= 0 {
			t.Errorf("%s should be > 0, got %d", name, val)
		}
	}
}

// TestFileSizeConstantsConsistent ensures derived file size constants line up.
func TestFileSizeConstantsConsistent(t *testing.T) {
	if BytesPerToken <= 0 {
		t.Errorf("BytesPerToken should be > 0, got %d", BytesPerToken)
	}
	if MaxToolResultTokens <= 0 {
		t.Errorf("MaxToolResultTokens should be > 0, got %d", MaxToolResultTokens)
	}
	if MaxToolResultBytes != MaxToolResultTokens*BytesPerToken {
		t.Errorf("MaxToolResultBytes (%d) should equal MaxToolResultTokens*BytesPerToken (%d)",
			MaxToolResultBytes, MaxToolResultTokens*BytesPerToken)
	}
	if DefaultMaxResultSizeChars <= 0 {
		t.Error("DefaultMaxResultSizeChars should be > 0")
	}
	if MaxToolResultsPerMessageChars < DefaultMaxResultSizeChars {
		t.Errorf("MaxToolResultsPerMessageChars (%d) should be >= DefaultMaxResultSizeChars (%d)",
			MaxToolResultsPerMessageChars, DefaultMaxResultSizeChars)
	}
	if BinaryCheckSize <= 0 {
		t.Error("BinaryCheckSize should be > 0")
	}
	if ToolSummaryMaxLength <= 0 {
		t.Error("ToolSummaryMaxLength should be > 0")
	}
}

// TestBinaryExtensionsAndHelper exercises the BinaryExtensions map and helper.
func TestBinaryExtensionsAndHelper(t *testing.T) {
	if len(BinaryExtensions) == 0 {
		t.Fatal("BinaryExtensions should not be empty")
	}
	for ext := range BinaryExtensions {
		if !strings.HasPrefix(ext, ".") {
			t.Errorf("binary extension %q should start with a dot", ext)
		}
		if ext != strings.ToLower(ext) {
			t.Errorf("binary extension %q should be lowercase", ext)
		}
	}
	// Helper checks
	binaryCases := []string{"foo.png", "BAR.PNG", "/tmp/x.exe", "image.JPEG", "data.zip"}
	for _, p := range binaryCases {
		if !HasBinaryExtension(p) {
			t.Errorf("HasBinaryExtension(%q) = false, want true", p)
		}
	}
	textCases := []string{"foo.go", "main.py", "README.md", "no_ext", ""}
	for _, p := range textCases {
		if HasBinaryExtension(p) {
			t.Errorf("HasBinaryExtension(%q) = true, want false", p)
		}
	}
}

// TestIsBinaryContent exercises the binary-content detector.
func TestIsBinaryContent(t *testing.T) {
	if IsBinaryContent([]byte("hello world\nplain text\t.")) {
		t.Error("plain text should not be detected as binary")
	}
	if !IsBinaryContent([]byte{0x00, 0x01, 0x02, 0x03}) {
		t.Error("data with null byte should be detected as binary")
	}
	if IsBinaryContent(nil) {
		t.Error("nil/empty data should not be detected as binary")
	}
}

// TestIgnoredDirectories validates IgnoredDirectories and helper.
func TestIgnoredDirectories(t *testing.T) {
	if len(IgnoredDirectories) == 0 {
		t.Fatal("IgnoredDirectories should not be empty")
	}
	mustHave := []string{".git", "node_modules", "vendor", "dist", "build"}
	for _, d := range mustHave {
		if !IgnoredDirectories[d] {
			t.Errorf("IgnoredDirectories should contain %q", d)
		}
		if !IsIgnoredDirectory(d) {
			t.Errorf("IsIgnoredDirectory(%q) = false, want true", d)
		}
	}
	if IsIgnoredDirectory("src") {
		t.Error("IsIgnoredDirectory(\"src\") = true, want false")
	}
	if IsIgnoredDirectory("") {
		t.Error("IsIgnoredDirectory(\"\") = true, want false")
	}
}

// TestErrorConstants verifies error IDs, categories and message templates.
func TestErrorConstants(t *testing.T) {
	if EToolUseSummaryGenerationFailed <= 0 {
		t.Errorf("EToolUseSummaryGenerationFailed should be > 0")
	}
	if NextErrorID <= EToolUseSummaryGenerationFailed {
		t.Errorf("NextErrorID (%d) should be > EToolUseSummaryGenerationFailed (%d)",
			NextErrorID, EToolUseSummaryGenerationFailed)
	}

	categories := []ErrorCategory{
		ErrorCategoryAPI, ErrorCategoryTool, ErrorCategoryPermission,
		ErrorCategoryConfig, ErrorCategoryNetwork, ErrorCategoryFileSystem,
		ErrorCategoryAuth, ErrorCategoryTask, ErrorCategoryMCP, ErrorCategoryInternal,
	}
	seen := map[ErrorCategory]bool{}
	for _, c := range categories {
		if c == "" {
			t.Error("error category should be non-empty")
		}
		if seen[c] {
			t.Errorf("duplicate error category %q", c)
		}
		seen[c] = true
	}

	msgs := map[string]string{
		"ErrMsgNoContent":           ErrMsgNoContent,
		"ErrMsgTaskNotFound":        ErrMsgTaskNotFound,
		"ErrMsgTaskNotRunning":      ErrMsgTaskNotRunning,
		"ErrMsgUnsupportedTaskType": ErrMsgUnsupportedTaskType,
		"ErrMsgSkillNotFound":       ErrMsgSkillNotFound,
		"ErrMsgAPIRateLimited":      ErrMsgAPIRateLimited,
		"ErrMsgAPIOverloaded":       ErrMsgAPIOverloaded,
		"ErrMsgAuthExpired":         ErrMsgAuthExpired,
		"ErrMsgMaxMediaExceeded":    ErrMsgMaxMediaExceeded,
		"ErrMsgFileTooLarge":        ErrMsgFileTooLarge,
		"ErrMsgBinaryFile":          ErrMsgBinaryFile,
		"ErrMsgPermissionDenied":    ErrMsgPermissionDenied,
		"ErrMsgToolTimeout":         ErrMsgToolTimeout,
		"ErrMsgContextOverflow":     ErrMsgContextOverflow,
	}
	for name, val := range msgs {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}

	// Templates with format verbs should contain the verb.
	if !strings.Contains(ErrMsgTaskNotFound, "%s") {
		t.Error("ErrMsgTaskNotFound should contain a string format verb")
	}
	if !strings.Contains(ErrMsgMaxMediaExceeded, "%d") {
		t.Error("ErrMsgMaxMediaExceeded should contain a decimal format verb")
	}

	stopCodes := []StopTaskErrorCode{
		StopTaskNotFound, StopTaskNotRunning, StopTaskUnsupportedType,
	}
	for _, c := range stopCodes {
		if c == "" {
			t.Error("stop task error code should be non-empty")
		}
	}
}

// TestXMLTagsAndFigures verifies XML tag constants and figure glyphs are populated.
func TestXMLTagsAndFigures(t *testing.T) {
	xmlTags := map[string]string{
		"XMLCommandName":          XMLCommandName,
		"XMLCommandMessage":       XMLCommandMessage,
		"XMLCommandArgs":          XMLCommandArgs,
		"XMLBashInput":            XMLBashInput,
		"XMLBashStdout":           XMLBashStdout,
		"XMLBashStderr":           XMLBashStderr,
		"XMLLocalCommandStdout":   XMLLocalCommandStdout,
		"XMLLocalCommandStderr":   XMLLocalCommandStderr,
		"XMLLocalCommandCaveat":   XMLLocalCommandCaveat,
		"XMLTick":                 XMLTick,
		"XMLTaskNotification":     XMLTaskNotification,
		"XMLTaskID":               XMLTaskID,
		"XMLToolUseID":            XMLToolUseID,
		"XMLTaskType":             XMLTaskType,
		"XMLOutputFile":           XMLOutputFile,
		"XMLStatus":               XMLStatus,
		"XMLSummary":              XMLSummary,
		"XMLReason":               XMLReason,
		"XMLWorktree":             XMLWorktree,
		"XMLWorktreePath":         XMLWorktreePath,
		"XMLWorktreeBranch":       XMLWorktreeBranch,
		"XMLUltraplan":            XMLUltraplan,
		"XMLRemoteReview":         XMLRemoteReview,
		"XMLRemoteReviewProgress": XMLRemoteReviewProgress,
		"XMLTeammateMessage":      XMLTeammateMessage,
		"XMLChannelMessage":       XMLChannelMessage,
		"XMLChannel":              XMLChannel,
		"XMLCrossSessionMessage":  XMLCrossSessionMessage,
		"XMLForkBoilerplate":      XMLForkBoilerplate,
		"ForkDirectivePrefix":     ForkDirectivePrefix,
	}
	for name, val := range xmlTags {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}

	// Cross-reference: every tag in TerminalOutputTags must be a known XML tag value.
	knownXMLValues := map[string]bool{}
	for _, v := range xmlTags {
		knownXMLValues[v] = true
	}
	if len(TerminalOutputTags) == 0 {
		t.Error("TerminalOutputTags should not be empty")
	}
	for _, tag := range TerminalOutputTags {
		if !knownXMLValues[tag] {
			t.Errorf("TerminalOutputTags contains unknown XML tag %q", tag)
		}
	}

	figures := []string{
		FigBlackCircle, FigBulletOperator, FigTeardropAsterisk,
		FigUpArrow, FigDownArrow, FigLightningBolt,
		FigEffortLow, FigEffortMedium, FigEffortHigh, FigEffortMax,
		FigPlayIcon, FigPauseIcon, FigRefreshArrow,
		FigChannelArrow, FigInjectedArrow, FigForkGlyph,
		FigDiamondOpen, FigDiamondFilled, FigReferenceMark,
		FigFlagIcon, FigBlockquoteBar, FigHeavyHorizontal,
		FigBridgeReady, FigBridgeFailed,
	}
	for i, f := range figures {
		if f == "" {
			t.Errorf("figure at index %d is empty", i)
		}
	}

	if len(BridgeSpinnerFrames) == 0 {
		t.Error("BridgeSpinnerFrames should not be empty")
	}
	for i, frame := range BridgeSpinnerFrames {
		if frame == "" {
			t.Errorf("BridgeSpinnerFrames[%d] is empty", i)
		}
	}

	if len(CommonHelpArgs) == 0 {
		t.Error("CommonHelpArgs should not be empty")
	}
	if len(CommonInfoArgs) == 0 {
		t.Error("CommonInfoArgs should not be empty")
	}
}

// TestPromptConstants validates prompt-related strings and word lists.
func TestPromptConstants(t *testing.T) {
	prompts := map[string]string{
		"SystemPromptDynamicBoundary": SystemPromptDynamicBoundary,
		"CyberRiskInstruction":        CyberRiskInstruction,
		"DefaultAgentPrompt":          DefaultAgentPrompt,
		"NoToolsPreamble":             NoToolsPreamble,
		"NoToolsTrailer":              NoToolsTrailer,
		"SummarizeToolResultsSection": SummarizeToolResultsSection,
	}
	for name, val := range prompts {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}

	if len(SpinnerVerbs) == 0 {
		t.Fatal("SpinnerVerbs should not be empty")
	}
	for i, v := range SpinnerVerbs {
		if v == "" {
			t.Errorf("SpinnerVerbs[%d] is empty", i)
		}
	}

	if len(TurnCompletionVerbs) == 0 {
		t.Fatal("TurnCompletionVerbs should not be empty")
	}
	for i, v := range TurnCompletionVerbs {
		if v == "" {
			t.Errorf("TurnCompletionVerbs[%d] is empty", i)
		}
	}
}

// TestFrontierModelIDsConsistent ensures frontier model IDs are populated and
// consistent with the ProductName / ProductNameCLI naming.
func TestFrontierModelIDsConsistent(t *testing.T) {
	models := map[string]string{
		"FrontierModelName": FrontierModelName,
		"ModelOpus46":       ModelOpus46,
		"ModelSonnet46":     ModelSonnet46,
		"ModelHaiku45":      ModelHaiku45,
	}
	for name, val := range models {
		if val == "" {
			t.Errorf("%s should be non-empty", name)
		}
	}
	// ModelHaiku45 should embed the haiku family identifier from the context-window map.
	if !strings.Contains(ModelHaiku45, "claude-haiku-4-5") {
		t.Errorf("ModelHaiku45 (%q) should contain claude-haiku-4-5", ModelHaiku45)
	}
	// ProductName should appear in the default sysprompt prefix.
	if !strings.Contains(DefaultCLISyspromptPrefix, ProductName) {
		t.Errorf("DefaultCLISyspromptPrefix should mention ProductName (%q)", ProductName)
	}
	// ConfigFileName / ConfigDirName should reflect the CLI name in uppercase form.
	upper := strings.ToUpper(ProductNameCLI)
	if !strings.HasPrefix(ConfigFileName, upper) {
		t.Errorf("ConfigFileName (%q) should start with %q", ConfigFileName, upper)
	}
}
