//go:build windows

package audit

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsAuditPrivateSecurityDescriptorRejectsBroadAndInheritedACLs(t *testing.T) {
	owner, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []bool{false, true} {
		descriptor, err := newWindowsAuditPrivateDescriptor(owner, directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyWindowsAuditPrivateDescriptor(descriptor, owner, directory); err != nil {
			t.Fatalf("private descriptor rejected: %v", err)
		}
	}

	world, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	broadACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		windowsAuditPrivateAccessEntry(owner, false),
		windowsAuditPrivateAccessEntry(world, false),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	broad, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := broad.SetOwner(owner, false); err != nil {
		t.Fatal(err)
	}
	if err := broad.SetDACL(broadACL, true, false); err != nil {
		t.Fatal(err)
	}
	if err := broad.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsAuditPrivateDescriptor(broad, owner, false); err == nil {
		t.Fatal("broad DACL accepted")
	}

	unprotected, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	privateACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{windowsAuditPrivateAccessEntry(owner, false)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := unprotected.SetOwner(owner, false); err != nil {
		t.Fatal(err)
	}
	if err := unprotected.SetDACL(privateACL, true, false); err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsAuditPrivateDescriptor(unprotected, owner, false); err == nil {
		t.Fatal("inherited/unprotected DACL accepted")
	}
}

func TestWindowsAuditPrivateMaskAcceptsGenericAndMappedFullControlOnly(t *testing.T) {
	if !windowsAuditPrivateMaskIsFullControl(windows.ACCESS_MASK(windows.GENERIC_ALL)) {
		t.Fatal("GENERIC_ALL was not accepted")
	}
	if !windowsAuditPrivateMaskIsFullControl(windowsAuditMappedFullControl) {
		t.Fatal("mapped file full control was not accepted")
	}
	if windowsAuditPrivateMaskIsFullControl(windows.ACCESS_MASK(windows.FILE_GENERIC_READ)) {
		t.Fatal("read-only access was accepted as full control")
	}
}

func TestWindowsAuditPrivateSecurityAppliesToRealFileAndDirectory(t *testing.T) {
	private, err := newWindowsAuditPrivateSecurity()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := openWindowsAuditAbsolutePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(parent) }()
	directory, err := openWindowsAuditDirectoryAt(
		parent,
		"private-dir",
		windows.FILE_OPEN_IF,
		windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE,
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(directory) }()
	file, err := openWindowsAuditFile(
		parent,
		"private.jsonl",
		windows.FILE_CREATE,
		windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.SYNCHRONIZE,
		private,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	for name, handle := range map[string]windows.Handle{"directory": directory, "file": windows.Handle(file.Fd())} {
		descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatalf("%s descriptor: %v", name, err)
		}
		if err := verifyWindowsAuditPrivateDescriptor(descriptor, private.owner, name == "directory"); err != nil {
			t.Fatalf("%s privacy: %v", name, err)
		}
	}
}
