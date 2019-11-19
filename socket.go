package socket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"net"
	"time"
	"unicode"

	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

type datacallback func([]byte)
type eventcallback func() // socket can not connect

// Socket interface
type Socket interface {
	SendMessage(act interface{})
	RecvMessage() []byte
	RecvMessageChan() <-chan []byte

	Close()
}

// socket 对接收的数据有两种处理方式:
// 1. 请求响应模式, 直接将数据塞入 channel 中, callback 参数传 nil
// 2. 使用回调直接处理数据, 需要传入 callback 参数
type socket struct {
	socketFile string

	cancel context.CancelFunc // 是不是要加锁保护一下??
	quit   chan interface{}

	sendChan chan interface{}
	recvChan chan []byte
	datacb   datacallback
	eventcb  eventcallback
}

// NewSocket 新建客户端连接
func NewSocket(socks string, callback datacallback, eventcb eventcallback) Socket {
	sock := socket{
		socketFile: socks,
		datacb:     callback,
		eventcb:    eventcb,
		sendChan:   make(chan interface{}, 10),
		recvChan:   make(chan []byte, 10*100),
		quit:       make(chan interface{}, 1),
	}

	// 启动
	go sock.translateLoop()
	return &sock
}

func (s *socket) SendMessage(act interface{}) {
	s.sendChan <- act
}

func (s *socket) RecvMessage() []byte {
	return <-s.recvChan
}

func (s *socket) RecvMessageChan() <-chan []byte {
	return s.recvChan
}

func (s *socket) Close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.quit <- true
}

func (s *socket) translateLoop() {
	for {
		select {
		case <-s.quit:
			return
		default:
			var uconn *net.UnixConn
			ctx, cancel := context.WithCancel(context.Background())
			group, errCtx := errgroup.WithContext(ctx)
			s.cancel = cancel

			uaddr, err := net.ResolveUnixAddr("unix", s.socketFile)
			if err != nil {
				goto StartErr
			}
			uconn, err = net.DialUnix("unix", nil, uaddr)
			if err != nil {
				//通知守护线程
				goto StartErr
			}
			group.Go(func() error {
				return s.sendLoop(errCtx, uconn)
			})
			group.Go(func() error {
				return s.receiveLoop(errCtx, uconn)
			})
			err = group.Wait()
			log.Println(err)
			uconn.Close()
		StartErr:
			if s.cancel != nil {
				s.cancel()
				s.cancel = nil
			}
			if s.eventcb != nil {
				// socket 连不上了
				// 通知处理一下
				s.eventcb()
			}
			time.Sleep(time.Millisecond * 500)
		}
	}
}

func (s *socket) sendLoop(ctx context.Context, conn *net.UnixConn) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-s.sendChan:
			data, err := json.Marshal(msg)
			if err != nil {
				log.Printf("sending data [%#v] marshal error: %s\n", msg, err.Error())
				continue
			}
			data = append(data, '\r', '\n')
			_, err = conn.Write(data)
			if err != nil {
				log.Println("send failed: ", err.Error())
				select {
				case s.sendChan <- msg:
				default:
				}
				return err
			}
			log.Println("send success: ", data)
		}
	}
}

func (s *socket) receiveLoop(ctx context.Context, conn *net.UnixConn) error {
	scanner := bufio.NewScanner(conn)
	scanner.Split(bufio.ScanLines)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if scanner.Scan() {
				data := scanner.Bytes()
				data = bytes.TrimFunc(data, func(data rune) bool {
					if unicode.IsSpace(data) {
						return true
					}
					if data == '\x00' {
						return true
					}
					return false
				})
				if len(data) == 0 {
					continue
				}
				if s.datacb != nil {
					s.datacb(data)
				} else {
					log.Printf("receive data: %s", data)
					s.recvChan <- data
				}
			} else {
				return errors.New("receive failed")
			}
		}
	}
}
