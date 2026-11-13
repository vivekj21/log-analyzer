package llm

// Forget passes cleanup through to the wrapped model.
func (r *Recorder) Forget(invID string) {
	if f, ok := any(r.wrapped).(Forgetter); ok {
		f.Forget(invID)
	}
}
