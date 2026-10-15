//go:build windows

package media

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var jobDLL = syscall.NewLazyDLL("kernel32.dll")

type jobHandle syscall.Handle

var ErrProcessStopUnconfirmed = errors.New("controlled process tree stop was not confirmed")

func (h jobHandle) Close() error {
	// Closing a kill-on-close Job requests termination but is not itself a
	// stopped fact. Observe zero active processes before publishing cancellation.
	ok, _, _ := jobDLL.NewProc("TerminateJobObject").Call(uintptr(h), 1)
	if ok == 0 {
		_ = syscall.CloseHandle(syscall.Handle(h))
		return ErrProcessStopUnconfirmed
	}
	type accounting struct {
		User, Kernel, PeriodUser, PeriodKernel int64
		Faults, Total, Active, Terminated      uint32
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var info accounting
		ok, _, _ := jobDLL.NewProc("QueryInformationJobObject").Call(uintptr(h), 1, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
		if ok == 0 {
			_ = syscall.CloseHandle(syscall.Handle(h))
			return ErrProcessStopUnconfirmed
		}
		if info.Active == 0 {
			return syscall.CloseHandle(syscall.Handle(h))
		}
		if time.Now().After(deadline) {
			_ = syscall.CloseHandle(syscall.Handle(h))
			return ErrProcessStopUnconfirmed
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type basicLimits struct {
	PerProcess, PerJob     int64
	Flags                  uint32
	MinWorking, MaxWorking uintptr
	Active                 uint32
	Affinity               uintptr
	Priority, Scheduling   uint32
}
type ioCounters struct{ ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes uint64 }
type extendedLimits struct {
	Basic                                          basicLimits
	IO                                             ioCounters
	ProcessMemory, JobMemory, PeakProcess, PeakJob uintptr
}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000004, HideWindow: true}
}

// Start suspended so even a very short ffprobe cannot exit (or spawn an
// uncontained child) between Start and AssignProcessToJobObject.
func resumeProcess(process *os.Process) error {
	snapshot, _, err := jobDLL.NewProc("CreateToolhelp32Snapshot").Call(0x00000004, 0)
	if snapshot == ^uintptr(0) {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))
	type threadEntry struct {
		Size, Usage, ID, Owner uint32
		Base, Delta            int32
		Flags                  uint32
	}
	entry := threadEntry{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	ok, _, _ := jobDLL.NewProc("Thread32First").Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.Owner == uint32(process.Pid) {
			thread, _, err := jobDLL.NewProc("OpenThread").Call(0x0002, 0, uintptr(entry.ID))
			if thread == 0 {
				return err
			}
			defer syscall.CloseHandle(syscall.Handle(thread))
			count, _, err := jobDLL.NewProc("ResumeThread").Call(thread)
			if uint32(count) == 0xffffffff {
				return err
			}
			return nil
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, _ = jobDLL.NewProc("Thread32Next").Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	return errors.New("suspended process thread was not found")
}

func bindProcessTree(process *os.Process) (io.Closer, error) {
	job, _, err := jobDLL.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return nil, err
	}
	h := jobHandle(job)
	limits := extendedLimits{}
	limits.Basic.Flags = 0x2000
	ok, _, err := jobDLL.NewProc("SetInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		h.Close()
		return nil, err
	}
	p, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(process.Pid))
	if err != nil {
		h.Close()
		return nil, err
	}
	defer syscall.CloseHandle(p)
	ok, _, err = jobDLL.NewProc("AssignProcessToJobObject").Call(job, uintptr(p))
	if ok == 0 {
		h.Close()
		return nil, err
	}
	return h, nil
}
