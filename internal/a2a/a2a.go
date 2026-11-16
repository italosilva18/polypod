// Package a2a — A2A Protocol from GEMINI-CLI
package a2a

type A2AServer struct {
	address string
}

func NewA2AServer(address string) *A2AServer {
	return &A2AServer{address: address}
}

func (s *A2AServer) Start() error {
	return nil
}

func (s *A2AServer) Stop() error {
	return nil
}
