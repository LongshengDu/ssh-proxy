package proxy

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
	"os/user"
)

func grantServiceControl(name string) error {
	current, err := user.Current()
	if err != nil {
		return err
	}
	sid, err := windows.StringToSid(current.Uid)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.SERVICE_QUERY_CONFIG | windows.SERVICE_QUERY_STATUS | windows.SERVICE_START | windows.SERVICE_STOP,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)},
	}}, old)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(name, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func ServiceAutoStart(name, action string) (bool, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, err
	}
	manager := &mgr.Mgr{Handle: h}
	defer manager.Disconnect()
	access := uint32(windows.SERVICE_QUERY_CONFIG)
	if action != "status" {
		access |= windows.SERVICE_CHANGE_CONFIG
	}
	n, _ := windows.UTF16PtrFromString(name)
	sh, err := windows.OpenService(h, n, access)
	if err != nil {
		return false, err
	}
	s := &mgr.Service{Name: name, Handle: sh}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return false, err
	}
	if action != "status" {
		cfg.StartType = windows.SERVICE_DEMAND_START
		cfg.DelayedAutoStart = false
		if action == "enable" {
			cfg.StartType = windows.SERVICE_AUTO_START
			cfg.DelayedAutoStart = true
		}
		if err = s.UpdateConfig(cfg); err != nil {
			return false, err
		}
	}
	return cfg.StartType == windows.SERVICE_AUTO_START, nil
}
