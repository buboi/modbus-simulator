package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

const (
	functionReadCoils          = 0x01
	functionReadDiscreteInputs = 0x02
	functionReadHoldingRegs    = 0x03
	functionReadInputRegs      = 0x04

	exceptionIllegalFunction = 0x01
	exceptionIllegalDataAddr = 0x02
	exceptionIllegalDataVal  = 0x03
)

var (
	errOutOfRange    = errors.New("out of range")
	errInvalidQty    = errors.New("invalid quantity")
	errInvalidPDULen = errors.New("invalid pdu length")
)

// Server implements a minimal Modbus TCP server that supports read functions.
type Server struct {
	listener  net.Listener
	wg        sync.WaitGroup
	quit      chan struct{}
	closeOnce sync.Once

	mu               sync.RWMutex
	HoldingRegisters []uint16
	InputRegisters   []uint16
	Coils            []bool
	DiscreteInputs   []bool
}

// NewServer constructs a server with default register sizes.
func NewServer() *Server {
	return &Server{
		HoldingRegisters: make([]uint16, 65536),
		InputRegisters:   make([]uint16, 65536),
		Coils:            make([]bool, 65536),
		DiscreteInputs:   make([]bool, 65536),
		quit:             make(chan struct{}),
	}
}

// Listen starts accepting Modbus TCP connections on the provided address.
func (s *Server) Listen(address string) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	s.listener = l

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
			}
			continue
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	header := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		length := binary.BigEndian.Uint16(header[4:6])
		if length == 0 {
			continue
		}

		pduLength := int(length - 1)
		if pduLength <= 0 {
			continue
		}

		unitID := header[6]
		pdu := make([]byte, pduLength)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}

		response := s.handlePDU(pdu)
		if len(response) == 0 {
			continue
		}

		binary.BigEndian.PutUint16(header[2:4], 0)
		binary.BigEndian.PutUint16(header[4:6], uint16(len(response)+1))
		header[6] = unitID

		if _, err := conn.Write(header); err != nil {
			return
		}
		if _, err := conn.Write(response); err != nil {
			return
		}
	}
}

func (s *Server) handlePDU(pdu []byte) []byte {
	if len(pdu) == 0 {
		return exceptionResponse(0, exceptionIllegalFunction)
	}

	function := pdu[0]
	switch function {
	case functionReadCoils:
		data, err := s.readBits(s.Coils, pdu)
		if err != nil {
			return exceptionResponse(function, errToCode(err))
		}
		return append([]byte{function, byte(len(data))}, data...)
	case functionReadDiscreteInputs:
		data, err := s.readBits(s.DiscreteInputs, pdu)
		if err != nil {
			return exceptionResponse(function, errToCode(err))
		}
		return append([]byte{function, byte(len(data))}, data...)
	case functionReadHoldingRegs:
		data, err := s.readRegisters(s.HoldingRegisters, pdu)
		if err != nil {
			return exceptionResponse(function, errToCode(err))
		}
		return append([]byte{function, byte(len(data))}, data...)
	case functionReadInputRegs:
		data, err := s.readRegisters(s.InputRegisters, pdu)
		if err != nil {
			return exceptionResponse(function, errToCode(err))
		}
		return append([]byte{function, byte(len(data))}, data...)
	default:
		return exceptionResponse(function, exceptionIllegalFunction)
	}
}

func (s *Server) readBits(source []bool, pdu []byte) ([]byte, error) {
	if len(pdu) < 5 {
		return nil, errInvalidPDULen
	}
	start := binary.BigEndian.Uint16(pdu[1:3])
	quantity := binary.BigEndian.Uint16(pdu[3:5])
	if quantity == 0 || quantity > 2000 {
		return nil, errInvalidQty
	}
	end := int(start) + int(quantity)
	if end > len(source) {
		return nil, errOutOfRange
	}

	byteCount := (int(quantity) + 7) / 8
	result := make([]byte, byteCount)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := 0; i < int(quantity); i++ {
		if source[int(start)+i] {
			result[i/8] |= 1 << (uint(i) % 8)
		}
	}
	return result, nil
}

func (s *Server) readRegisters(source []uint16, pdu []byte) ([]byte, error) {
	if len(pdu) < 5 {
		return nil, errInvalidPDULen
	}
	start := binary.BigEndian.Uint16(pdu[1:3])
	quantity := binary.BigEndian.Uint16(pdu[3:5])
	if quantity == 0 || quantity > 125 {
		return nil, errInvalidQty
	}
	end := int(start) + int(quantity)
	if end > len(source) {
		return nil, errOutOfRange
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]byte, quantity*2)
	for i := 0; i < int(quantity); i++ {
		binary.BigEndian.PutUint16(result[i*2:(i+1)*2], source[int(start)+i])
	}
	return result, nil
}

func exceptionResponse(function byte, code byte) []byte {
	if function == 0 {
		function = 0x80
	} else {
		function = function | 0x80
	}
	return []byte{function, code}
}

func errToCode(err error) byte {
	switch {
	case errors.Is(err, errOutOfRange):
		return exceptionIllegalDataAddr
	case errors.Is(err, errInvalidQty):
		return exceptionIllegalDataVal
	case errors.Is(err, errInvalidPDULen):
		return exceptionIllegalDataVal
	default:
		return exceptionIllegalFunction
	}
}

// Close stops the server and waits for all goroutines to exit.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.quit)
		if s.listener != nil {
			s.listener.Close()
		}
	})
	s.wg.Wait()
}

// SetHoldingRegister updates a holding register value.
func (s *Server) SetHoldingRegister(address uint16, value uint16) error {
	if int(address) >= len(s.HoldingRegisters) {
		return fmt.Errorf("address %d out of range", address)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HoldingRegisters[address] = value
	return nil
}

// SetInputRegister updates an input register value.
func (s *Server) SetInputRegister(address uint16, value uint16) error {
	if int(address) >= len(s.InputRegisters) {
		return fmt.Errorf("address %d out of range", address)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InputRegisters[address] = value
	return nil
}

// SetCoil updates a coil value.
func (s *Server) SetCoil(address uint16, value bool) error {
	if int(address) >= len(s.Coils) {
		return fmt.Errorf("address %d out of range", address)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Coils[address] = value
	return nil
}

// SetDiscreteInput updates a discrete input value.
func (s *Server) SetDiscreteInput(address uint16, value bool) error {
	if int(address) >= len(s.DiscreteInputs) {
		return fmt.Errorf("address %d out of range", address)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DiscreteInputs[address] = value
	return nil
}
