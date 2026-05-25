package server

import "context"

type Notification struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type notifierKey struct{}

type Notifier interface {
	Notify(Notification)
}

type notifierFunc func(Notification)

func (f notifierFunc) Notify(n Notification) {
	f(n)
}

func WithNotifier(ctx context.Context, fn func(Notification)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, notifierKey{}, notifierFunc(fn))
}

func Notify(ctx context.Context, n Notification) bool {
	v := ctx.Value(notifierKey{})
	notifier, ok := v.(Notifier)
	if !ok || notifier == nil {
		return false
	}
	notifier.Notify(n)
	return true
}

func NotifyProgress(ctx context.Context, progressToken interface{}, progress float64, total float64, message string) bool {
	params := map[string]interface{}{
		"progressToken": progressToken,
		"progress":      progress,
	}
	if total > 0 {
		params["total"] = total
	}
	if message != "" {
		params["message"] = message
	}
	return Notify(ctx, Notification{
		Method: "notifications/progress",
		Params: params,
	})
}

func NotifyMessage(ctx context.Context, level, message string) bool {
	params := map[string]interface{}{
		"level":   level,
		"message": message,
	}
	return Notify(ctx, Notification{
		Method: "notifications/message",
		Params: params,
	})
}
