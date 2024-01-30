package chan_watcher

import (
	"errors"
	"reflect"
	"sync"
	"time"
)

var (
	ErrParam             = errors.New("input param illegal")
	ErrNotChan           = errors.New("input param is not reflect.Chan")
	ErrNotReceivableChan = errors.New("chan is not a receivable")
	ErrReceiverFull      = errors.New("chan receiver is full. (chans>=65535)")
	ErrExplictQuit       = errors.New("explicit quit")
)

// 回调函数返回任何错误，chan会被移除不再监听。如果不知道返回啥错误，可以返回 ErrExplictQuit
type Callback func(Event) error

type Event struct {
	Val interface{} // chan收到的数据
	Ok  bool        // chan接收是否成功，false表示chan被关闭，此时Val值无效
	P   interface{} // AddChan时传入的回调参数
}

type Chan struct {
	p  interface{} // 回调参数
	fn Callback
}

type ChanWatcher struct {
	sync.Once
	chanVal []Chan               // 回调
	chans   []reflect.SelectCase // 工作区
	waiting []reflect.SelectCase // 等候区
	sync.Mutex
}

func (mcw *ChanWatcher) AddChan(c interface{}, fn Callback, p interface{}) error {
	// 参数检查
	if c == nil || fn == nil {
		return ErrParam
	}

	tc := reflect.TypeOf(c)
	if tc.Kind() != reflect.Chan {
		return ErrNotChan
	}

	if tc.ChanDir()&reflect.RecvDir == 0 {
		// 非可读chan
		return ErrNotReceivableChan
	}

	mcw.Do(func() {
		// 初始化
		mcw.chanVal = make([]Chan, 1, 64)
		mcw.chans = make([]reflect.SelectCase, 0, 64)
		mcw.chans = append(mcw.chans, reflect.SelectCase{
			Dir: reflect.SelectDefault, // 添加default
		})
		go mcw.run()
	})

	mcw.Lock()
	defer mcw.Unlock()

	if len(mcw.chans)+len(mcw.waiting)+1 >= 65535 {
		return ErrReceiverFull
	}

	mcw.chanVal = append(mcw.chanVal, Chan{fn: fn, p: p})
	mcw.waiting = append(mcw.waiting, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(c),
	})

	return nil
}

func (mcw *ChanWatcher) run() {
	for {
		chosen, recv, recvOk := reflect.Select(mcw.chans)
		c := mcw.chans[chosen]
		if c.Dir == reflect.SelectDefault {
			// default case
			mcw.Lock()
			if len(mcw.waiting) > 0 {
				mcw.chans = append(mcw.chans, mcw.waiting...)
				mcw.waiting = mcw.waiting[:0]
			}
			mcw.Unlock()
			time.Sleep(time.Millisecond * 10)
		} else {
			var err error
			if recvOk {
				mcw.Lock()
				v := mcw.chanVal[chosen]
				mcw.Unlock()

				err = v.fn(Event{Val: recv.Interface(), Ok: recvOk, P: v.p})
				if err == nil {
					continue
				}
			}

			// chan closed / initiatively finish, remove it
			mcw.Lock()
			mcw.chans = append(mcw.chans[:chosen], mcw.chans[chosen+1:]...)
			v := mcw.chanVal[chosen]
			mcw.chanVal = append(mcw.chanVal[:chosen], mcw.chanVal[chosen+1:]...)
			mcw.Unlock()

			if err == nil {
				err = v.fn(Event{Val: recv.Interface(), Ok: recvOk, P: v.p})
				// trivial err，chan already removed
			} else {
				// 回调中主动要求关闭，不做通知处理
			}
		}
	}
}
