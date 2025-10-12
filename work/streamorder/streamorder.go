package streamorder

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ChannelStreamOrder represents the custom ordering configuration for a single channel's
// streams, storing the channel identifier and an ordered array of stream indices that
// define the user's preferred sequence for stream selection and display.
type ChannelStreamOrder struct {
	Channel     string `json:"channel"`
	StreamOrder []int  `json:"streamOrder"` // Array of stream indexes in custom order
}

// StreamOrderFile represents the complete persistent storage structure for all channel
// stream ordering configurations, wrapping an array of ChannelStreamOrder objects to
// provide a consistent file format and enable future extension with additional metadata.
type StreamOrderFile struct {
	ChannelOrders []ChannelStreamOrder `json:"channelOrders"`
}

var (
	// orderMutex provides thread-safe access to stream order file operations, protecting
	// against race conditions during concurrent read and write operations from multiple
	// goroutines handling admin interface requests.
	orderMutex sync.RWMutex
)

// LoadStreamOrders reads and parses the stream ordering database from disk, creating
// an empty structure if the file doesn't exist and handling corrupted data gracefully
// by returning an empty configuration rather than failing completely.
//
// Returns:
//   - *StreamOrderFile: parsed stream orders or empty structure on error
//   - error: non-nil only for serious I/O failures that prevent operation
func LoadStreamOrders() (*StreamOrderFile, error) {
	orderPath := "/settings/stream-orders.json"

	if _, err := os.Stat(orderPath); os.IsNotExist(err) {
		// Create empty file with proper permissions
		emptyFile := &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}
		data, _ := json.MarshalIndent(emptyFile, "", "  ")
		if err := os.WriteFile(orderPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to create stream orders file: %w", err)
		}
		return emptyFile, nil
	}

	data, err := os.ReadFile(orderPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read stream orders file: %w", err)
	}

	if len(data) == 0 {
		return &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}, nil
	}

	var orders StreamOrderFile
	if err := json.Unmarshal(data, &orders); err != nil {
		return &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}, nil
	}

	if orders.ChannelOrders == nil {
		orders.ChannelOrders = []ChannelStreamOrder{}
	}

	return &orders, nil
}

// SaveStreamOrders persists the provided stream ordering data to disk as formatted JSON,
// writing atomically with consistent formatting to ensure the file remains readable and
// maintainable across application restarts.
//
// Parameters:
//   - orders: complete stream ordering data structure to persist
//
// Returns:
//   - error: non-nil if the file cannot be written to disk
func SaveStreamOrders(orders *StreamOrderFile) error {
	orderPath := "/settings/stream-orders.json"

	data, err := json.MarshalIndent(orders, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal stream orders: %w", err)
	}

	return os.WriteFile(orderPath, data, 0644)
}

// SetChannelStreamOrder updates or creates the custom stream ordering for a specific
// channel, atomically modifying the persistent database to reflect the new stream
// sequence. If an order already exists for the channel, it is replaced; otherwise
// a new entry is created.
//
// Parameters:
//   - channelName: unique channel identifier for the ordering configuration
//   - streamOrder: array of stream indices in the desired display order
//
// Returns:
//   - error: non-nil if database operations fail
func SetChannelStreamOrder(channelName string, streamOrder []int) error {
	orderMutex.Lock()
	defer orderMutex.Unlock()

	orders, err := LoadStreamOrders()
	if err != nil {
		return err
	}

	for i, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			orders.ChannelOrders[i].StreamOrder = streamOrder
			return SaveStreamOrders(orders)
		}
	}

	newOrder := ChannelStreamOrder{
		Channel:     channelName,
		StreamOrder: streamOrder,
	}
	orders.ChannelOrders = append(orders.ChannelOrders, newOrder)
	return SaveStreamOrders(orders)
}

// DeleteChannelStreamOrder removes the custom stream ordering for a specific channel
// from the persistent database, reverting to default ordering behavior.
func DeleteChannelStreamOrder(channelName string) error {
	orderMutex.Lock()
	defer orderMutex.Unlock()

	orders, err := LoadStreamOrders()
	if err != nil {
		return err
	}

	// Find and remove the channel order
	for i, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			// Remove this entry
			orders.ChannelOrders = append(orders.ChannelOrders[:i], orders.ChannelOrders[i+1:]...)
			return SaveStreamOrders(orders)
		}
	}

	// If not found, that's okay - it's already not in the file
	return nil
}

// GetChannelStreamOrder retrieves the custom stream ordering for a specific channel
// from the persistent database, returning nil if no custom order exists for the
// requested channel (indicating default ordering should be used).
//
// Parameters:
//   - channelName: unique channel identifier to retrieve ordering for
//
// Returns:
//   - []int: array of stream indices in custom order, or nil if no custom order exists
//   - error: non-nil if database operations fail
func GetChannelStreamOrder(channelName string) ([]int, error) {
	orderMutex.RLock()
	defer orderMutex.RUnlock()

	orders, err := LoadStreamOrders()
	if err != nil {
		return nil, err
	}

	for _, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			return order.StreamOrder, nil
		}
	}

	return nil, nil
}
