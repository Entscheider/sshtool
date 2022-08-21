package sshport

import (
	"context"
	"fmt"
	"github.com/Entscheider/sshtool/logger"
	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
	"net"
	"strconv"
	"sync"
	"time"
)

// This file contains utility objects for handling a tcp/ip forward for ssh.
// It does this by implementing the [net.Listener] and [net.Conn] interface to interact
// with the ssh connection. Thus, no real port on the host system must be opened.

// SSHConnectionHandler is a helper struct for creating [net.Listener] that can be forwarded with ssh.
// It should be used in the following manner:
//
//	func sshTcpIpTest(logger logger.Logger, ctx context.Context) {
//		var tcpipHandler = NewSSHConnectionHandler(logger, ctx)
//		var sshServer = &ssh.Server{
//			LocalPortForwardingCallback: func(ctx ssh.Context, destinationHost string, destinationPort uint32) bool {
//				return true
//			},
//		}
//		// Add it as tcpip handler in the ssh connection
//		sshServer.ChannelHandlers = map[string]ssh.ChannelHandler{
//			"session":      ssh.DefaultSessionHandler,
//			"direct-tcpip": tcpipHandler.HandleTCPIP,
//		}
//		// We now create a simple http server that the user "peter" can forward from port 8080
//		listener := tcpipHandler.CreateListener(8080, "peter")
//		server := http.Server{}
//		go server.Serve(listener)
//	}
type SSHConnectionHandler struct {
	// TODO: Map by username rather than port?
	// A map which collect for every ports all active tcp/ip connection (for every user)
	listeners map[uint32][]*sshConnectionListener
	// Mutex used when modifying the listeners map
	listenersMutex sync.Mutex
	// For logging errors and infos
	logger logger.Logger
	// The context (e.g. for canceling)
	ctx context.Context
}

// NewSSHConnectionHandler creates a new SSHConnectionHandler in the given ctx and logs with the given logger.
func NewSSHConnectionHandler(logger logger.Logger, ctx context.Context) SSHConnectionHandler {
	return SSHConnectionHandler{
		logger:    logger,
		ctx:       ctx,
		listeners: map[uint32][]*sshConnectionListener{},
	}
}

// HandleTCPIP implements a direct-tcpip [ssh.ChannelHandler] for forward a tcp/ip connection through ssh.
// This code is highly copied from
// https://github.com/gliderlabs/ssh/blob/30ec06db4e743ac9f827a69c8b8cfb84064a6dc7/tcpip.go#L28=
func (s *SSHConnectionHandler) HandleTCPIP(srv *gssh.Server, conn *ssh.ServerConn, newChan ssh.NewChannel, ctx gssh.Context) {
	// This is used to parse the tcpip forward parameter
	type localForwardChannelData struct {
		DestAddr string
		DestPort uint32

		OriginAddr string
		OriginPort uint32
	}

	// Parse the forwarding parameters
	d := localForwardChannelData{}
	if err := ssh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		s.logger.Info("HandleTCPIP", "forbid tcpip as we cannot unmarshal data: "+err.Error())
		err := newChan.Reject(ssh.ConnectionFailed, "error parsing forward data: "+err.Error())
		if err != nil {
			s.logger.Err("HandleTCPIP", err.Error())
		}
		return
	}

	// Rejecting invalid parameters
	if srv.LocalPortForwardingCallback == nil || !srv.LocalPortForwardingCallback(ctx, d.DestAddr, d.DestPort) {
		s.logger.Info("HandleTCPIP", "forbid tcpip as srv port-warding was not set ")
		err := newChan.Reject(ssh.Prohibited, "port forwarding is disabled")
		if err != nil {
			s.logger.Err("HandleTCPIP", err.Error())
		}
		return
	}

	// Reject forwards to any other than localhost
	if d.DestAddr != "localhost" && d.DestAddr != "127.0.0.1" {
		s.logger.Info("HandleTCPIP", fmt.Sprintf("forbid tcpip because requested addr (%s) ist not localhost", d.DestAddr))
		err := newChan.Reject(ssh.Prohibited, fmt.Sprintf("destination %s host is not allowed", d.DestAddr))
		if err != nil {
			s.logger.Err("HandleTCPIP", err.Error())
		}
		return
	}

	// We obtain the appropriate sshConnectionListener object that handles the tcp/ip communication for this port.
	listeners, ok := s.listeners[d.DestPort]
	if !ok || len(listeners) == 0 {
		s.logger.Info("HandleTCPIP", fmt.Sprintf("forbid tcpip as no listener was found for the port %d", d.DestPort))
		err := newChan.Reject(ssh.Prohibited, fmt.Sprintf("port %d cannot be forwarded", d.DestPort))
		if err != nil {
			s.logger.Err("HandleTCPIP", err.Error())
		}
		return
	}

	pair := sshConnectionChannelPair{newChan, conn, ctx}

	s.listenersMutex.Lock()
	defer s.listenersMutex.Unlock()

	var firstListener *sshConnectionListener

	// We get the sshConnectionListener for this user and notify it about the new forward request.
	// If there are multiple, we try to get the first one that is pending. Otherwise, we wait
	// for the first one found until it is ready (see the end of this function)
	for _, listener := range listeners {
		if listener.user == ctx.User() || listener.user == "" {
			if firstListener == nil {
				firstListener = listener
			}
			if listener.isPending {
				err := listener.haveNewChannel(pair)
				if err != nil {
					s.logger.Err("SSHConnectionHandler", err.Error())
				} else {
					s.logger.Info("SSHConnectionHandler", fmt.Sprintf("forwarded %v", d))
					return
				}
			}
		}
	}
	if firstListener == nil {
		s.logger.Info("HandleTCPIP", fmt.Sprintf("forbid tcpip as no listener was found for the user %s at port %d", ctx.User(), d.DestPort))
		err := newChan.Reject(ssh.Prohibited, fmt.Sprintf("port %d cannot be forwarded", d.DestPort))
		if err != nil {
			s.logger.Err("HandleTCPIP", err.Error())
		}
		return
	}

	err := firstListener.haveNewChannel(pair)
	if err != nil {
		s.logger.Err("SSHConnectionHandler", err.Error())
	} else {
		s.logger.Info("SSHConnectionHandler", fmt.Sprintf("forwarded %v", d))
	}
}

// CreateListener creates a new [net.Listener] for the given user at the specific port.
// So only this user can forward this [net.Listener] with ssh.
// If user is empty, this listener is active for all users.
func (s *SSHConnectionHandler) CreateListener(port uint32, user string) net.Listener {
	s.listenersMutex.Lock()
	defer s.listenersMutex.Unlock()
	listener := &sshConnectionListener{
		user:           user,
		port:           port,
		newChannelChan: make(chan sshConnectionChannelPair, 1),
		isPending:      true,
		parent:         s,
		ctx:            s.ctx,
	}
	// We add the listener info in the map
	listeners, ok := s.listeners[port]
	if !ok {
		s.listeners[port] = []*sshConnectionListener{listener}
	} else {
		listeners = append(listeners, listener)
		s.listeners[port] = listeners
	}
	return listener
}

func (s *SSHConnectionHandler) removeListener(listener *sshConnectionListener) {
	s.listenersMutex.Lock()
	defer s.listenersMutex.Unlock()

	port := listener.port
	listeners, ok := s.listeners[port]
	if !ok {
		return
	}
	i := -1

	for j, connectionListener := range listeners {
		if connectionListener == listener {
			i = j
			break
		}
	}

	if i >= 0 {
		listeners = append(listeners[:i], listeners[i+1:]...)
		s.listeners[port] = listeners
	}
}

// Describes an established tcp/ip forward request. This is supposed to be sent to a
// [sshConnectionListener] that handles the communication.
type sshConnectionChannelPair struct {
	// The channel to
	channel    ssh.NewChannel
	connection *ssh.ServerConn
	ctx        gssh.Context
}

// sshConnectionListener is [net.Listener] implementation that virtually creates a port and forward
// it to the ssh connection if desired.
// When accepting a connection, this listener creates a [sshConnectionWrapper], which implements [net.Conn].
type sshConnectionListener struct {
	// The user this virtual tcp/ip connection is for. Can be empty to be served to all users.
	user string
	// The port that can be forwarded.
	port uint32
	// The channel for communicating with it is waiting for accepting a new connection.
	newChannelChan chan sshConnectionChannelPair
	// isPending is true while a new [sshConnectionPair] has been sent but is not yet received.
	// This means that a new ssh tcp/ip forwarding has been established but the [net.Listener] accept
	// method was not called yet or is still processing another request.
	isPending bool
	// The [SSHConnectionHandler] which has created this struct.
	parent *SSHConnectionHandler
	// The context that can be used e.g. for canceling.
	ctx context.Context
}

// Sends the [sshConnectionChannelPair] to this [sshConnectionListener] and set it as not-pending
// until [sshConnectionListener] has received the pair.
func (s *sshConnectionListener) haveNewChannel(pair sshConnectionChannelPair) error {
	s.isPending = false
	defer func() { s.isPending = true }()
	select {
	case s.newChannelChan <- pair:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *sshConnectionListener) Accept() (net.Conn, error) {
	select {
	case pair := <-s.newChannelChan:
		ch, reqs, err := pair.channel.Accept()
		if err != nil {
			return nil, err
		}
		go ssh.DiscardRequests(reqs)
		return &sshConnectionWrapper{inner: ch, ctx: pair.ctx}, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *sshConnectionListener) Close() error {
	s.parent.removeListener(s)
	close(s.newChannelChan)
	return nil
}

func (s *sshConnectionListener) Addr() net.Addr {
	return fakeAddr{
		s.port,
	}
}

// [net.Addr] implementation which always resolves to localhost:port
type fakeAddr struct {
	port uint32
}

func (f fakeAddr) Network() string {
	return "ssh/tcp"
}

func (f fakeAddr) String() string {
	return fmt.Sprintf("localhost: %s", strconv.FormatInt(int64(f.port), 10))
}

// [net.Conn] implementation that writes to a tcp/ip forwarding ssh channel.
type sshConnectionWrapper struct {
	inner ssh.Channel
	ctx   gssh.Context
}

func (s sshConnectionWrapper) Read(b []byte) (n int, err error) {
	return s.inner.Read(b)
}

func (s sshConnectionWrapper) Write(b []byte) (n int, err error) {
	return s.inner.Write(b)
}

func (s sshConnectionWrapper) Close() error {
	return s.inner.Close()
}

func (s sshConnectionWrapper) LocalAddr() net.Addr {
	return s.ctx.LocalAddr()
}

func (s sshConnectionWrapper) RemoteAddr() net.Addr {
	return s.ctx.RemoteAddr()
}

func (s sshConnectionWrapper) SetDeadline(t time.Time) error {
	err := s.SetReadDeadline(t)
	if err != nil {
		return err
	}
	err = s.SetWriteDeadline(t)
	if err != nil {
		return err
	}
	return nil
}

func (s sshConnectionWrapper) SetReadDeadline(_ time.Time) error {
	//panic("implement me")
	// TODO: Not implemented
	return nil
}

func (s sshConnectionWrapper) SetWriteDeadline(_ time.Time) error {
	//panic("implement me")
	// TODO: Not implemented
	return nil
}
