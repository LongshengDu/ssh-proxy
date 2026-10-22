//go:build windows || darwin

package proxy

import "github.com/kardianos/service"

func servicePresent(string) (bool, error)    { return true, nil }
func configureService(*service.Config) error { return nil }
