package computeruse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ─── Computer use tool ─────────────────────────────────────────────────

// ComputerUseTool implements the tool interface for computer interaction.
type ComputerUseTool struct {
	mu              sync.Mutex
	service         *ComputerUseService
	lastScreenshot  []byte
	lastScreenTime  time.Time
	screenshotCache time.Duration
	rateLimit       time.Duration
	lastAction      time.Time
}

// NewComputerUseTool creates a new computer use tool.
func NewComputerUseTool() *ComputerUseTool {
	return &ComputerUseTool{
		service:         NewComputerUseService(),
		screenshotCache: 2 * time.Second,
		rateLimit:       100 * time.Millisecond,
	}
}

// ToolAction represents a computer use action.
type ToolAction struct {
	Action    string `json:"action"`
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
	Text      string `json:"text,omitempty"`
	Key       string `json:"key,omitempty"`
	Button    string `json:"button,omitempty"`
	Direction string `json:"direction,omitempty"`
	Amount    int    `json:"amount,omitempty"`
	ToX       int    `json:"to_x,omitempty"`
	ToY       int    `json:"to_y,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// ToolResult is the result of executing a computer use action.
type ToolResult struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	Screenshot string `json:"screenshot,omitempty"` // base64-encoded image
	Error      string `json:"error,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// Execute runs a computer use action.
func (t *ComputerUseTool) Execute(ctx context.Context, actionJSON string) (*ToolResult, error) {
	var action ToolAction
	if err := json.Unmarshal([]byte(actionJSON), &action); err != nil {
		return nil, fmt.Errorf("invalid action JSON: %w", err)
	}

	return t.ExecuteAction(ctx, action)
}

// ExecuteAction runs a parsed computer use action with safety checks.
func (t *ComputerUseTool) ExecuteAction(ctx context.Context, action ToolAction) (*ToolResult, error) {
	// Rate limiting.
	t.mu.Lock()
	if !t.lastAction.IsZero() {
		elapsed := time.Since(t.lastAction)
		if elapsed < t.rateLimit {
			time.Sleep(t.rateLimit - elapsed)
		}
	}
	t.lastAction = time.Now()
	t.mu.Unlock()

	// Coordinate validation for mouse actions.
	if needsCoordinates(action.Action) {
		if err := t.validateCoordinates(action.X, action.Y); err != nil {
			return &ToolResult{Success: false, Error: err.Error()}, nil
		}
	}

	switch action.Action {
	case "screenshot":
		return t.actionScreenshot(ctx)
	case "click":
		return t.actionClick(ctx, action)
	case "type":
		return t.actionType(ctx, action)
	case "key_press", "key":
		return t.actionKeyPress(ctx, action)
	case "scroll":
		return t.actionScroll(ctx, action)
	case "move_mouse":
		return t.actionMoveMouse(ctx, action)
	case "drag":
		return t.actionDrag(ctx, action)
	case "double_click":
		return t.actionDoubleClick(ctx, action)
	case "right_click":
		return t.actionRightClick(ctx, action)
	default:
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("unknown action: %s", action.Action),
		}, nil
	}
}

func needsCoordinates(action string) bool {
	switch action {
	case "click", "double_click", "right_click", "move_mouse", "scroll", "drag":
		return true
	}
	return false
}

func (t *ComputerUseTool) validateCoordinates(x, y int) error {
	display, err := t.service.GetDisplay()
	if err != nil {
		// Can't validate if display info unavailable — allow through.
		return nil
	}

	if x < 0 || y < 0 {
		return fmt.Errorf("coordinates (%d, %d) are negative", x, y)
	}
	if x > display.Width || y > display.Height {
		return fmt.Errorf("coordinates (%d, %d) are outside screen bounds (%dx%d)",
			x, y, display.Width, display.Height)
	}
	return nil
}

// ─── Action implementations ───────────────────────────────────────────

func (t *ComputerUseTool) actionScreenshot(ctx context.Context) (*ToolResult, error) {
	// Check cache.
	t.mu.Lock()
	if t.lastScreenshot != nil && time.Since(t.lastScreenTime) < t.screenshotCache {
		cached := t.lastScreenshot
		t.mu.Unlock()
		return &ToolResult{
			Success:    true,
			Screenshot: base64.StdEncoding.EncodeToString(cached),
			Message:    "Screenshot captured (cached)",
		}, nil
	}
	t.mu.Unlock()

	data, err := CaptureScreen(ctx)
	if err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	// Update cache.
	t.mu.Lock()
	t.lastScreenshot = data
	t.lastScreenTime = time.Now()
	t.mu.Unlock()

	return &ToolResult{
		Success:    true,
		Screenshot: base64.StdEncoding.EncodeToString(data),
		Message:    "Screenshot captured",
	}, nil
}

func (t *ComputerUseTool) actionClick(ctx context.Context, action ToolAction) (*ToolResult, error) {
	button := action.Button
	if button == "" {
		button = "left"
	}
	count := action.Count
	if count <= 0 {
		count = 1
	}

	if err := MouseClickN(ctx, action.X, action.Y, button, count); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	// Invalidate screenshot cache after action.
	t.invalidateCache()

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Clicked %s at (%d, %d)", button, action.X, action.Y),
	}, nil
}

func (t *ComputerUseTool) actionDoubleClick(ctx context.Context, action ToolAction) (*ToolResult, error) {
	if err := MouseClickN(ctx, action.X, action.Y, "left", 2); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}
	t.invalidateCache()
	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Double-clicked at (%d, %d)", action.X, action.Y),
	}, nil
}

func (t *ComputerUseTool) actionRightClick(ctx context.Context, action ToolAction) (*ToolResult, error) {
	if err := MouseClick(ctx, action.X, action.Y, "right"); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}
	t.invalidateCache()
	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Right-clicked at (%d, %d)", action.X, action.Y),
	}, nil
}

func (t *ComputerUseTool) actionType(ctx context.Context, action ToolAction) (*ToolResult, error) {
	if action.Text == "" {
		return &ToolResult{Success: false, Error: "text is required for type action"}, nil
	}

	if err := TypeText(ctx, action.Text); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	t.invalidateCache()

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Typed %d characters", len(action.Text)),
	}, nil
}

func (t *ComputerUseTool) actionKeyPress(ctx context.Context, action ToolAction) (*ToolResult, error) {
	key := action.Key
	if key == "" {
		key = action.Text
	}
	if key == "" {
		return &ToolResult{Success: false, Error: "key is required for key_press action"}, nil
	}

	if err := KeyPressAction(ctx, key); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	t.invalidateCache()

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Pressed key: %s", key),
	}, nil
}

func (t *ComputerUseTool) actionScroll(ctx context.Context, action ToolAction) (*ToolResult, error) {
	direction := action.Direction
	if direction == "" {
		direction = "down"
	}
	amount := action.Amount
	if amount <= 0 {
		amount = 3
	}

	if err := MouseScroll(ctx, action.X, action.Y, direction, amount); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	t.invalidateCache()

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Scrolled %s %d clicks at (%d, %d)", direction, amount, action.X, action.Y),
	}, nil
}

func (t *ComputerUseTool) actionMoveMouse(ctx context.Context, action ToolAction) (*ToolResult, error) {
	if err := MouseMove(ctx, action.X, action.Y); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Moved mouse to (%d, %d)", action.X, action.Y),
	}, nil
}

func (t *ComputerUseTool) actionDrag(ctx context.Context, action ToolAction) (*ToolResult, error) {
	if err := Drag(ctx, action.X, action.Y, action.ToX, action.ToY); err != nil {
		return &ToolResult{Success: false, Error: err.Error()}, nil
	}

	t.invalidateCache()

	return &ToolResult{
		Success: true,
		Message: fmt.Sprintf("Dragged from (%d, %d) to (%d, %d)",
			action.X, action.Y, action.ToX, action.ToY),
	}, nil
}

func (t *ComputerUseTool) invalidateCache() {
	t.mu.Lock()
	t.lastScreenshot = nil
	t.mu.Unlock()
}
