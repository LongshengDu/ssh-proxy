//go:build windows || darwin

package service

import kardianos "github.com/kardianos/service"

func servicePresent(string) (bool, error)      { return true, nil }
func configureService(*kardianos.Config) error { return nil }
