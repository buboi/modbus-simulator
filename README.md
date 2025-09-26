# Modbus Simulator

This repository provides a lightweight Modbus TCP server simulator written in Go.
Register addresses and CSV data bindings are configured using a TOML file. The
simulator reads data from a CSV file and periodically updates the Modbus
registers with the configured values.

## Features

- Minimal Modbus TCP server that supports reading coils, discrete inputs,
  holding registers, and input registers.
- TOML configuration to define server settings, update interval, and register
  bindings.
- CSV-driven data updates that cycle through rows on a fixed interval.

## Getting Started

1. **Install Go** – version 1.20 or later is recommended.
2. **Clone** this repository and change into the project directory.
3. **Create a configuration file** based on `config.example.toml`.
4. **Prepare a CSV file** containing the columns referenced in the
   configuration.

```
# Example
cp config.example.toml config.toml
```

The example configuration references `data/example_data.csv`, which contains
sample telemetry rows.

## Running the simulator

```
go run ./cmd/server --config config.toml
```

The server listens on the address configured in the TOML file (default `:1502`)
and will rotate through the CSV rows on the configured interval, updating the
Modbus register values accordingly.

Use any Modbus TCP client to read from the configured registers. The simulator
supports the following Modbus function codes:

- `0x01` – Read Coils
- `0x02` – Read Discrete Inputs
- `0x03` – Read Holding Registers
- `0x04` – Read Input Registers

## Configuration Reference

- `csv_file`: Path to the CSV file containing data rows.
- `update_interval`: Go duration string that controls how often registers are
  updated (e.g., `"5s"`).
- `[server]` section:
  - `listen_address`: TCP address for the Modbus server (default `:1502`).
- `[[registers]]` array:
  - `type`: Register type (`holding`, `input`, `coil`, or `discrete`).
  - `address`: Register address to update.
  - `csv_column`: Column name in the CSV file to use for the register value.

The simulator loops through the CSV rows continuously. Coil and discrete input
values treat any non-zero CSV value as `true`.
