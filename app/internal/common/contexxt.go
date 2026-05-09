package common

type ContextKey int

const (
	ContextConfig ContextKey = iota
)

func (k ContextKey) String() string {
	switch k {
	case ContextConfig:
		return "config"
	default:
		return "unknown"
	}
}
