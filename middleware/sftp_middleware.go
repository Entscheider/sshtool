package middleware

import (
	"github.com/Entscheider/sshtool/logger"
	"github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	"io"
	"log"
)

// Handler is a function that handles a new connection and creates the desired sftp.Handlers filesystem
// to serve for this connection.
type Handler func(info logger.ConnectionInfo) sftp.Handlers

// A function that wraps the given handler into an ssh.SubsystemHandler and logs access using the accessLogger.
func subsystemHandler(handler Handler, accessLogger logger.AccessLogger) ssh.SubsystemHandler {
	return func(s ssh.Session) {
		// Create the meta information object.
		info := logger.ConnectionInfo{
			Username: s.User(),
			IP:       s.RemoteAddr().String(),
		}
		accessLogger.NewLogin(info, "granted")
		// Create a new sftp server that handles this connection using the filesystem from the handler.
		server := sftp.NewRequestServer(s, handler(info))
		// A channel whose closing signals that the sftp connection has ended.
		servingChan := make(chan bool)
		// Serving the client in a separate go routine.
		go func() {
			defer close(servingChan)
			err := server.Serve()
			if err != nil && err != io.EOF {
				log.Printf("Error %v", err)
			}
		}()
		// And close the server when requested.
		select {
		case <-s.Context().Done():
			err := server.Close()
			if err != nil {
				log.Printf("Error %v", err)
			}
		case <-servingChan:

		}
		accessLogger.Logout(info)
	}
}

// AddSftpSubsystemHandler adds a handler for the sftp protocol to the given handlers by using the given handler function
// that creates a sftp.Handlers instance for every new connection happening.
// The logger parameter is used to log errors and accesses.
func AddSftpSubsystemHandler(handler Handler, logger logger.AccessLogger, handlers map[string]ssh.SubsystemHandler) map[string]ssh.SubsystemHandler {
	handlers["sftp"] = subsystemHandler(handler, logger)
	return handlers
}
