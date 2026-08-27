//go:build !windows

package skills

func requireSkillMutationSupported() error { return nil }
