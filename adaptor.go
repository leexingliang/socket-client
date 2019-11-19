package socket

import (
	"sync"
	"time"

	"github.com/pkg/errors"
)

// PreProcessCallback 预处理回调
type PreProcessCallback func([]byte) (interface{}, error)

// Adaptor socket 请求响应模式包装
type Adaptor struct {
	socket Socket

	timeout   int
	processcb PreProcessCallback

	lock sync.Mutex
}

// NewAdaptor 新建适配器
func NewAdaptor(timeout int, processcb PreProcessCallback,
	socketfile string, callback datacallback, eventcb eventcallback,
) *Adaptor {
	sock := NewSocket(socketfile, callback, eventcb)

	return &Adaptor{
		socket:    sock,
		processcb: processcb,
		timeout:   timeout,
	}
}

// NewAdaptorWithSocket 新建适配器
func NewAdaptorWithSocket(sock Socket, timeout int, processcb PreProcessCallback) *Adaptor {
	return &Adaptor{
		socket:    sock,
		processcb: processcb,
		timeout:   timeout,
	}
}

func (a *Adaptor) request(action interface{}) (interface{}, error) {
	a.lock.Lock()
	defer a.lock.Unlock()
	timeout := time.NewTimer(time.Duration(a.timeout) * time.Second)

	a.socket.SendMessage(&action)
	for {
		select {
		case <-timeout.C:
			return nil, errors.New("timeout")
		case data := <-a.socket.RecvMessageChan():
			if a.processcb != nil {
				return a.processcb(data)
			}
			return data, nil
		}
	}
}
