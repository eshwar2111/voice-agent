package automation

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// Simplified vtables for UIAutomation
type IUIAutomation struct {
	vtbl *IUIAutomationVtbl
}

type IUIAutomationVtbl struct {
	ole.IUnknownVtbl
	CompareElements             uintptr
	CompareRuntimeIds           uintptr
	GetRootElement              uintptr
	ElementFromHandle           uintptr
	ElementFromPoint            uintptr
	GetFocusedElement           uintptr
	GetRootElementBuildCache    uintptr
	ElementFromHandleBuildCache uintptr
	ElementFromPointBuildCache  uintptr
	GetFocusedElementBuildCache uintptr
	CreateTreeWalker            uintptr
	Get_ControlViewWalker       uintptr
	Get_ContentViewWalker       uintptr
	Get_RawViewWalker           uintptr
	Get_RawViewCondition        uintptr
	Get_ControlViewCondition    uintptr
	Get_ContentViewCondition    uintptr
	CreateCacheRequest          uintptr
	CreateTrueCondition         uintptr
	CreateFalseCondition        uintptr
	CreatePropertyCondition     uintptr
}

type IUIAutomationElement struct {
	vtbl *IUIAutomationElementVtbl
}

type IUIAutomationElementVtbl struct {
	ole.IUnknownVtbl
	SetFocus                        uintptr
	GetRuntimeId                    uintptr
	FindFirst                       uintptr
	FindAll                         uintptr
	FindFirstBuildCache             uintptr
	FindAllBuildCache               uintptr
	BuildUpdatedCache               uintptr
	GetCurrentPropertyValue         uintptr
	GetCurrentPropertyValueEx       uintptr
	GetCachedPropertyValue          uintptr
	GetCachedPropertyValueEx        uintptr
	GetCurrentPatternAs             uintptr
	GetCachedPatternAs              uintptr
	GetCurrentPattern               uintptr
	GetCachedPattern                uintptr
	GetCachedParent                 uintptr
	GetCachedChildren               uintptr
	Get_CurrentProcessId            uintptr
	Get_CurrentControlType          uintptr
	Get_CurrentLocalizedControlType uintptr
	Get_CurrentName                 uintptr
}

type IUIAutomationCondition struct {
	vtbl *IUIAutomationConditionVtbl
}
type IUIAutomationConditionVtbl struct {
	ole.IUnknownVtbl
}

type IUIAutomationInvokePattern struct {
	vtbl *IUIAutomationInvokePatternVtbl
}
type IUIAutomationInvokePatternVtbl struct {
	ole.IUnknownVtbl
	Invoke uintptr
}

var (
	modoleaut32        = syscall.NewLazyDLL("oleaut32.dll")
	procSysAllocString = modoleaut32.NewProc("SysAllocString")
	procSysFreeString  = modoleaut32.NewProc("SysFreeString")
)

func allocString(s string) uintptr {
	ptr, _ := syscall.UTF16PtrFromString(s)
	ret, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(ptr)))
	return ret
}

func freeString(ptr uintptr) {
	procSysFreeString.Call(ptr)
}

// ClickElementByName connects to the root desktop and finds a UI element by name, then Invokes it.
func ClickElementByName(name string) error {
	// Initialize COM for the current thread
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	if err != nil {
		// If it's already initialized, we continue
		if err.(*ole.OleError).Code() != 1 && err.(*ole.OleError).Code() != 0x80010106 {
			return fmt.Errorf("CoInitializeEx failed: %w", err)
		}
	}
	defer ole.CoUninitialize()

	// Instantiate CUIAutomation
	clsid, err := ole.CLSIDFromString("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	if err != nil {
		return fmt.Errorf("CLSIDFromString failed: %w", err)
	}

	unknown, err := ole.CreateInstance(clsid, ole.IID_IUnknown)
	if err != nil {
		return fmt.Errorf("CreateInstance failed: %w", err)
	}
	defer unknown.Release()

	// Query for IUIAutomation interface
	iid, _ := ole.IIDFromString("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
	disp, err := unknown.QueryInterface(iid)
	if err != nil {
		return fmt.Errorf("QueryInterface failed: %w", err)
	}
	defer disp.Release()

	uia := (*IUIAutomation)(unsafe.Pointer(disp))

	// Get root desktop element
	var rootElement *IUIAutomationElement
	ret, _, _ := syscall.SyscallN(uia.vtbl.GetRootElement, uintptr(unsafe.Pointer(uia)), uintptr(unsafe.Pointer(&rootElement)))
	if ret != 0 {
		return fmt.Errorf("GetRootElement failed: %x", ret)
	}
	if rootElement == nil {
		return fmt.Errorf("GetRootElement succeeded but returned no element")
	}

	// Create Property Condition (UIA_NamePropertyId = 30005)
	var condition *IUIAutomationCondition
	bstr := allocString(name)
	defer freeString(bstr)

	variant := ole.VARIANT{}
	variant.VT = ole.VT_BSTR
	variant.Val = int64(bstr)

	ret, _, _ = syscall.SyscallN(uia.vtbl.CreatePropertyCondition, uintptr(unsafe.Pointer(uia)), uintptr(30005), uintptr(unsafe.Pointer(&variant)), uintptr(unsafe.Pointer(&condition)))
	if ret != 0 {
		return fmt.Errorf("CreatePropertyCondition failed: %x", ret)
	}
	if condition == nil {
		return fmt.Errorf("CreatePropertyCondition succeeded but returned no condition")
	}

	// Find the first matching element (TreeScope_Descendants = 4)
	var foundElement *IUIAutomationElement
	ret, _, _ = syscall.SyscallN(rootElement.vtbl.FindFirst, uintptr(unsafe.Pointer(rootElement)), uintptr(4), uintptr(unsafe.Pointer(condition)), uintptr(unsafe.Pointer(&foundElement)))
	if ret != 0 {
		return fmt.Errorf("FindFirst failed: %x", ret)
	}
	if foundElement == nil {
		return fmt.Errorf("element with name '%s' not found", name)
	}

	// Query the InvokePattern (UIA_InvokePatternId = 10000)
	var pattern *IUIAutomationInvokePattern
	ret, _, _ = syscall.SyscallN(foundElement.vtbl.GetCurrentPattern, uintptr(unsafe.Pointer(foundElement)), uintptr(10000), uintptr(unsafe.Pointer(&pattern)))
	if ret != 0 || pattern == nil {
		return fmt.Errorf("found element '%s' but it does not support InvokePattern (clicking)", name)
	}

	// Programmatically Invoke (click) the element
	ret, _, _ = syscall.SyscallN(pattern.vtbl.Invoke, uintptr(unsafe.Pointer(pattern)))
	if ret != 0 {
		return fmt.Errorf("Invoke failed: %x", ret)
	}

	return nil
}
