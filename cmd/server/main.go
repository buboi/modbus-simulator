package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"modbus-simulator/internal/config"
	"modbus-simulator/internal/modbus"
)

type registerValue struct {
	regType string
	address uint16
	column  string
}

type simulator struct {
	cfg          config.Config
	server       *modbus.Server
	values       []registerValue
	dataRows     []map[string]uint16
	updatePeriod time.Duration
	mu           sync.Mutex
	rowIndex     int
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.toml", "Path to configuration file")
	flag.Parse()

	if err := run(configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	sim, err := newSimulator(cfg)
	if err != nil {
		return fmt.Errorf("create simulator: %w", err)
	}
	defer sim.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- sim.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down simulator")
		return nil
	case err := <-errCh:
		return err
	}
}

func newSimulator(cfg config.Config) (*simulator, error) {
	duration, err := time.ParseDuration(cfg.UpdateInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid update interval: %w", err)
	}

	server := modbus.NewServer()
	if err := server.Listen(cfg.Server.ListenAddress); err != nil {
		return nil, fmt.Errorf("start modbus server: %w", err)
	}

	values := make([]registerValue, len(cfg.Registers))
	for i, reg := range cfg.Registers {
		switch reg.Type {
		case "holding", "input", "coil", "discrete":
		default:
			server.Close()
			return nil, fmt.Errorf("unsupported register type %s", reg.Type)
		}
		values[i] = registerValue{regType: reg.Type, address: reg.Address, column: reg.CSVColumn}
	}

	rows, err := loadCSV(cfg.CSVFile)
	if err != nil {
		server.Close()
		return nil, fmt.Errorf("load csv: %w", err)
	}

	sim := &simulator{
		cfg:          cfg,
		server:       server,
		values:       values,
		dataRows:     rows,
		updatePeriod: duration,
	}

	return sim, nil
}

func loadCSV(path string) ([]map[string]uint16, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, errors.New("csv must contain header and at least one data row")
	}

	header := records[0]
	rows := make([]map[string]uint16, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != len(header) {
			return nil, errors.New("csv record length mismatch")
		}
		row := make(map[string]uint16, len(header))
		for i, key := range header {
			val, err := strconv.ParseUint(record[i], 10, 16)
			if err != nil {
				return nil, fmt.Errorf("invalid value for column %s: %w", key, err)
			}
			row[key] = uint16(val)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *simulator) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.updatePeriod)
	defer ticker.Stop()

	log.Printf("Modbus simulator listening on %s", s.cfg.Server.ListenAddress)

	s.applyRow(0)

	for {
		select {
		case <-ticker.C:
			s.nextRow()
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *simulator) nextRow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.dataRows) == 0 {
		return
	}

	s.rowIndex = (s.rowIndex + 1) % len(s.dataRows)
	s.applyRowLocked(s.rowIndex)
}

func (s *simulator) applyRow(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyRowLocked(index)
}

func (s *simulator) applyRowLocked(index int) {
	if len(s.dataRows) == 0 {
		return
	}
	row := s.dataRows[index]
	for _, value := range s.values {
		val, ok := row[value.column]
		if !ok {
			log.Printf("column %s not found in csv data", value.column)
			continue
		}
		switch value.regType {
		case "holding":
			if err := s.server.SetHoldingRegister(value.address, val); err != nil {
				log.Printf("set holding register: %v", err)
			}
		case "input":
			if err := s.server.SetInputRegister(value.address, val); err != nil {
				log.Printf("set input register: %v", err)
			}
		case "coil":
			if err := s.server.SetCoil(value.address, val > 0); err != nil {
				log.Printf("set coil: %v", err)
			}
		case "discrete":
			if err := s.server.SetDiscreteInput(value.address, val > 0); err != nil {
				log.Printf("set discrete input: %v", err)
			}
		default:
			log.Printf("unsupported register type %s", value.regType)
		}
	}
}

func (s *simulator) Close() {
	s.server.Close()
}
