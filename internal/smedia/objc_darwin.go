//go:build darwin

// 极简 Objective-C / Foundation 桥：macOS 的 Now Playing 集成要调
// MediaPlayer.framework 的 Objective-C 类，而本项目 macOS 产物以
// CGO_ENABLED=0 交叉编译，无法用 cgo + .m 文件，因此经 purego 的
// objc 包裸调 libobjc 的 objc_msgSend，框架类由 dlopen 注册。
package smedia

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	pathLibObjC      = "/usr/lib/libobjc.A.dylib"
	pathFoundation   = "/System/Library/Frameworks/Foundation.framework/Foundation"
	pathAppKit       = "/System/Library/Frameworks/AppKit.framework/AppKit"
	pathCoreGraphics = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	pathMediaPlayer  = "/System/Library/Frameworks/MediaPlayer.framework/MediaPlayer"
)

var (
	objcHandle        uintptr // libobjc.A
	mediaPlayerHandle uintptr // MediaPlayer.framework（符号常量 dlsym 用）

	autoreleasePush uintptr
	autoreleasePop  uintptr

	// 主线程投递（libdispatch / pthread），供 Now Playing 回写使用；
	// 解析失败不致命，调用方退化为同步回写。
	dispatchMainQueueFn uintptr
	dispatchAsyncFn     uintptr
	pthreadMainNPFn     uintptr

	objcInitOnce sync.Once
	objcInitErr  error
)

// loadObjC 打开 Now Playing 依赖的框架并解析自动释放池入口。幂等，
// 失败结果会被缓存（调用方退化为静态空实现即可）。
func loadObjC() error {
	objcInitOnce.Do(func() {
		objcInitErr = func() error {
			var err error
			if objcHandle, err = purego.Dlopen(pathLibObjC, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
				return fmt.Errorf("dlopen libobjc: %w", err)
			}
			// Foundation/AppKit 提供 NSString/NSNumber/NSDictionary/NSImage；
			// CoreGraphics 供 CGSize；MediaPlayer 提供 MP* 类与符号常量。
			for _, fw := range []string{pathFoundation, pathAppKit, pathCoreGraphics} {
				if _, err := purego.Dlopen(fw, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
					return fmt.Errorf("dlopen %s: %w", fw, err)
				}
			}
			if mediaPlayerHandle, err = purego.Dlopen(pathMediaPlayer, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
				return fmt.Errorf("dlopen MediaPlayer: %w", err)
			}
			if autoreleasePush, err = purego.Dlsym(objcHandle, "objc_autoreleasePoolPush"); err != nil {
				return fmt.Errorf("dlsym objc_autoreleasePoolPush: %w", err)
			}
			if autoreleasePop, err = purego.Dlsym(objcHandle, "objc_autoreleasePoolPop"); err != nil {
				return fmt.Errorf("dlsym objc_autoreleasePoolPop: %w", err)
			}
			// 尽力而为：缺失时 onMainThread 按真处理，直接同步回写。
			dispatchMainQueueFn, _ = purego.Dlsym(purego.RTLD_DEFAULT, "dispatch_get_main_queue")
			dispatchAsyncFn, _ = purego.Dlsym(purego.RTLD_DEFAULT, "dispatch_async")
			pthreadMainNPFn, _ = purego.Dlsym(purego.RTLD_DEFAULT, "pthread_main_np")
			return nil
		}()
	})
	return objcInitErr
}

var (
	selMu    sync.RWMutex
	selCache = map[string]objc.SEL{}
)

// sel 返回缓存的选择器。objc.RegisterName 会抓 Objective-C 全局锁，
// 热路径上缓存是必要的。
func sel(name string) objc.SEL {
	selMu.RLock()
	s, ok := selCache[name]
	selMu.RUnlock()
	if ok {
		return s
	}
	s = objc.RegisterName(name)
	selMu.Lock()
	selCache[name] = s
	selMu.Unlock()
	return s
}

// class 返回类对象；类不存在时返回 0（由调用方决定是否退化）。
func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

// nsString 返回 autoreleased NSString，需在 withPool 内调用。
func nsString(s string) objc.ID {
	if s == "" {
		return 0
	}
	buf := append([]byte(s), 0)
	str := class("NSString").Send(sel("stringWithUTF8String:"), unsafe.Pointer(&buf[0]))
	runtime.KeepAlive(buf)
	return str
}

// nsNumberDouble 返回 autoreleased NSNumber（double）。
func nsNumberDouble(v float64) objc.ID {
	return class("NSNumber").Send(sel("numberWithDouble:"), v)
}

// nsMutableDict 返回 autoreleased NSMutableDictionary。
func nsMutableDict(cap int) objc.ID {
	return class("NSMutableDictionary").Send(sel("dictionaryWithCapacity:"), cap)
}

// dictSet 在非 nil 值写入时调用 setObject:forKey:（NSDictionary 不接受 nil）。
func dictSet(dict, key, val objc.ID) {
	if dict != 0 && key != 0 && val != 0 {
		dict.Send(sel("setObject:forKey:"), val, key)
	}
}

var (
	fwStrMu    sync.RWMutex
	fwStrCache = map[string]objc.ID{}
)

// frameworkString 取框架导出的 NSString 常量（如 MPMediaItemPropertyTitle）。
// 这些常量的值是静态存活的对象，缓存后复用；符号缺失时按名字回退（回退值
// 是 autoreleased，不缓存）。
func frameworkString(handle uintptr, name string) objc.ID {
	fwStrMu.RLock()
	v, ok := fwStrCache[name]
	fwStrMu.RUnlock()
	if ok {
		return v
	}
	if handle != 0 {
		if p, err := purego.Dlsym(handle, name); err == nil && p != 0 {
			// 经 &p 取值，避免 unsafe.Pointer(uintptr) 触发 vet 的 unsafeptr 告警。
			if v := **(**objc.ID)(unsafe.Pointer(&p)); v != 0 {
				fwStrMu.Lock()
				fwStrCache[name] = v
				fwStrMu.Unlock()
				return v
			}
		}
	}
	return nsString(name)
}

// nsImageFromData 用图片字节建一个 owned NSImage；失败返回 0。
func nsImageFromData(b []byte) objc.ID {
	if len(b) == 0 {
		return 0
	}
	data := class("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&b[0]), len(b))
	runtime.KeepAlive(b)
	if data == 0 {
		return 0
	}
	return class("NSImage").Send(sel("alloc")).Send(sel("initWithData:"), data)
}

func objcRelease(o objc.ID) {
	if o != 0 {
		o.Send(sel("release"))
	}
}

// withPool 在自动释放池内运行 fn。池属于压栈线程，故期间锁定 OS 线程，
// 否则 Go 调度迁移会让 pop 落到别的线程而崩溃。
func withPool(fn func()) {
	if autoreleasePush == 0 {
		fn()
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(autoreleasePush)
	defer purego.SyscallN(autoreleasePop, pool)
	fn()
}

// onMainThread 报告当前 goroutine 是否跑在进程主线程。无法判定时按真处理，
// 让调用方走同步路径而不是把工作投进可能永不排空的主队列。
func onMainThread() bool {
	if pthreadMainNPFn == 0 {
		return true
	}
	r, _, _ := purego.SyscallN(pthreadMainNPFn)
	return r != 0
}

// runOnMainAsync 把 fn 投递到主队列且不等待（主队列由 [NSApp run] 的
// 主 run loop 排空）。libdispatch 不可用时返回 false。
func runOnMainAsync(fn func()) bool {
	if dispatchMainQueueFn == 0 || dispatchAsyncFn == 0 {
		return false
	}
	q, _, _ := purego.SyscallN(dispatchMainQueueFn)
	if q == 0 {
		return false
	}
	block := objc.NewBlock(func(_ objc.Block) { fn() })
	purego.SyscallN(dispatchAsyncFn, q, uintptr(block))
	block.Release() // dispatch_async 已 Block_copy，释放本函数持有的引用
	return true
}
