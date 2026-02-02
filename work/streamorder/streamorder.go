package streamorder

import (
	"encoding/json"
	"kptv-proxy/work/logger"
	"os"
	"sync"
)

/**
 * ChannelStreamOrder represents the custom ordering configuration for a single channel's
 * streams, storing the channel identifier and an ordered array of stream indices that
 * define the user's preferred sequence for stream selection and display.
 */
type ChannelStreamOrder struct {
	Channel     string `json:"channel"`
	StreamOrder []int  `json:"streamOrder"` // Array of stream indexes in custom order
}

/**
 * StreamOrderFile represents the complete persistent storage structure for all channel
 * stream ordering configurations, wrapping an array of ChannelStreamOrder objects to
 * provide a consistent file format and enable future extension with additional metadata.
 */
type StreamOrderFile struct {
	ChannelOrders []ChannelStreamOrder `json:"channelOrders"`
}

var (
	// orderMutex provides thread-safe access to stream order file operations, protecting
	// against race conditions during concurrent read and write operations from multiple
	// goroutines handling admin interface requests.
	orderMutex sync.RWMutex
)

/**
 * LoadStreamOrders reads and parses the stream ordering database from disk, creating
 * an empty structure if the file doesn't exist and handling corrupted data gracefully
 * by returning an empty configuration rather than failing completely.
 *
 * The function implements defensive file handling:
 * - Creates file with default structure if missing
 * - Returns empty structure for corrupted JSON
 * - Ensures ChannelOrders array is always initialized
 * - Sets appropriate file permissions (0644)
 *
 * @return (*StreamOrderFile, error) - parsed stream orders or empty structure, error on I/O failure
 */
func LoadStreamOrders() (*StreamOrderFile, error) {
	orderPath := "/settings/stream-orders.json"
	//logger.Debug("{stream/streamorder - LoadStreamOrders} Loading stream orders from: %s", orderPath)

	// Check if file exists, create if missing
	if _, err := os.Stat(orderPath); os.IsNotExist(err) {
		logger.Debug("{stream/streamorder - LoadStreamOrders} File does not exist, creating new stream orders file: %s", orderPath)

		// Create empty file with proper permissions
		emptyFile := &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}
		data, _ := json.MarshalIndent(emptyFile, "", "  ")
		if err := os.WriteFile(orderPath, data, 0644); err != nil {
			logger.Error("{stream/streamorder - LoadStreamOrders} Failed to create stream orders file: %v", err)
			return nil, err
		}
		logger.Debug("{stream/streamorder - LoadStreamOrders} Created new empty stream orders file")
		return emptyFile, nil
	}

	// Read existing file
	data, err := os.ReadFile(orderPath)
	if err != nil {
		logger.Error("{stream/streamorder - LoadStreamOrders} Failed to read stream orders file: %v", err)
		return nil, err
	}

	//logger.Debug("{stream/streamorder - HandleStreaLoadStreamOrdersmFailure} Read %d bytes from stream orders file", len(data))

	// Handle empty file
	if len(data) == 0 {
		logger.Debug("{stream/streamorder - LoadStreamOrders} File is empty, returning empty structure")
		return &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}, nil
	}

	// Parse JSON data
	var orders StreamOrderFile
	if err := json.Unmarshal(data, &orders); err != nil {
		logger.Warn("{stream/streamorder - LoadStreamOrders} Failed to parse stream orders JSON, returning empty structure: %v", err)
		return &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}, nil
	}

	// Ensure ChannelOrders array is initialized
	if orders.ChannelOrders == nil {
		logger.Debug("{stream/streamorder - LoadStreamOrders} ChannelOrders was nil, initializing empty array")
		orders.ChannelOrders = []ChannelStreamOrder{}
	}

	//logger.Debug("{stream/streamorder - LoadStreamOrders} Successfully loaded %d channel orders", len(orders.ChannelOrders))

	return &orders, nil
}

/**
 * SaveStreamOrders persists the provided stream ordering data to disk as formatted JSON,
 * writing atomically with consistent formatting to ensure the file remains readable and
 * maintainable across application restarts.
 *
 * The function uses indented JSON formatting for human readability and sets appropriate
 * file permissions to allow owner read/write and group/world read access.
 *
 * @param orders Complete stream ordering data structure to persist
 * @return error - non-nil if the file cannot be written to disk
 */
func SaveStreamOrders(orders *StreamOrderFile) error {
	orderPath := "/settings/stream-orders.json"
	logger.Debug("{stream/streamorder - SaveStreamOrders} Saving stream orders to: %s (%d channels)", orderPath, len(orders.ChannelOrders))

	// Marshal to formatted JSON
	data, err := json.MarshalIndent(orders, "", "  ")
	if err != nil {
		logger.Error("{stream/streamorder - SaveStreamOrders} Failed to marshal stream orders to JSON: %v", err)
		return err
	}

	logger.Debug("{stream/streamorder - SaveStreamOrders} Marshaled %d bytes of JSON data", len(data))

	// Write to file with appropriate permissions
	if err := os.WriteFile(orderPath, data, 0644); err != nil {
		logger.Error("{stream/streamorder - SaveStreamOrders} Failed to write stream orders file: %v", err)
		return err
	}

	logger.Debug("{stream/streamorder - SaveStreamOrders} Successfully saved stream orders file")
	return nil
}

/**
 * SetChannelStreamOrder updates or creates the custom stream ordering for a specific
 * channel, atomically modifying the persistent database to reflect the new stream
 * sequence.
 *
 * The function performs atomic update operations:
 * - Acquires exclusive write lock to prevent concurrent modifications
 * - Loads current state from disk
 * - Updates existing entry or creates new one
 * - Persists changes atomically
 *
 * If an order already exists for the channel, it is replaced; otherwise
 * a new entry is created.
 *
 * @param channelName Unique channel identifier for the ordering configuration
 * @param streamOrder Array of stream indices in the desired display order
 * @return error - non-nil if database operations fail
 */
func SetChannelStreamOrder(channelName string, streamOrder []int) error {
	logger.Debug("{stream/streamorder - SetChannelStreamOrder} Setting stream order for channel: %s (%d streams)", channelName, len(streamOrder))

	orderMutex.Lock()
	defer orderMutex.Unlock()

	// Load current orders
	orders, err := LoadStreamOrders()
	if err != nil {
		logger.Error("{stream/streamorder - SetChannelStreamOrder} Failed to load stream orders for update: %v", err)
		return err
	}

	// Check if order already exists for this channel
	for i, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			logger.Debug("{stream/streamorder - SetChannelStreamOrder} Updating existing order for channel: %s", channelName)
			orders.ChannelOrders[i].StreamOrder = streamOrder

			if err := SaveStreamOrders(orders); err != nil {
				logger.Error("{stream/streamorder - SetChannelStreamOrder} Failed to save updated stream order for channel %s: %v", channelName, err)
				return err
			}

			logger.Debug("{stream/streamorder - SetChannelStreamOrder} Successfully updated stream order for channel: %s", channelName)
			return nil
		}
	}

	// Create new order entry
	logger.Debug("{stream/streamorder - SetChannelStreamOrder} Creating new order entry for channel: %s", channelName)
	newOrder := ChannelStreamOrder{
		Channel:     channelName,
		StreamOrder: streamOrder,
	}
	orders.ChannelOrders = append(orders.ChannelOrders, newOrder)

	if err := SaveStreamOrders(orders); err != nil {
		logger.Error("{stream/streamorder - SetChannelStreamOrder} Failed to save new stream order for channel %s: %v", channelName, err)
		return err
	}

	logger.Debug("{stream/streamorder - SetChannelStreamOrder} Successfully created new stream order for channel: %s", channelName)
	return nil
}

/**
 * DeleteChannelStreamOrder removes the custom stream ordering for a specific channel
 * from the persistent database, reverting to default ordering behavior.
 *
 * The function performs atomic deletion:
 * - Acquires exclusive write lock
 * - Loads current state
 * - Removes matching entry if found
 * - Persists changes atomically
 * - Returns success even if entry doesn't exist (idempotent operation)
 *
 * @param channelName Unique channel identifier to remove ordering for
 * @return error - non-nil if database operations fail
 */
func DeleteChannelStreamOrder(channelName string) error {
	logger.Debug("{stream/streamorder - DeleteChannelStreamOrder} Deleting stream order for channel: %s", channelName)

	orderMutex.Lock()
	defer orderMutex.Unlock()

	// Load current orders
	orders, err := LoadStreamOrders()
	if err != nil {
		logger.Error("{stream/streamorder - DeleteChannelStreamOrder} Failed to load stream orders for deletion: %v", err)
		return err
	}

	// Find and remove the channel order
	for i, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			logger.Debug("{stream/streamorder - DeleteChannelStreamOrder} Found order entry for channel %s at index %d, removing", channelName, i)

			// Remove this entry
			orders.ChannelOrders = append(orders.ChannelOrders[:i], orders.ChannelOrders[i+1:]...)

			if err := SaveStreamOrders(orders); err != nil {
				logger.Error("{stream/streamorder - DeleteChannelStreamOrder} Failed to save after deleting order for channel %s: %v", channelName, err)
				return err
			}

			logger.Debug("{stream/streamorder - DeleteChannelStreamOrder} Successfully deleted stream order for channel: %s", channelName)
			return nil
		}
	}

	// If not found, that's okay - it's already not in the file (idempotent)
	logger.Debug("{stream/streamorder - DeleteChannelStreamOrder} No order entry found for channel %s, nothing to delete", channelName)
	return nil
}

/**
 * GetChannelStreamOrder retrieves the custom stream ordering for a specific channel
 * from the persistent database, returning nil if no custom order exists for the
 * requested channel (indicating default ordering should be used).
 *
 * The function uses read lock for concurrent access:
 * - Acquires shared read lock to allow concurrent readers
 * - Loads current state from disk
 * - Returns matching order or nil if not found
 *
 * @param channelName Unique channel identifier to retrieve ordering for
 * @return ([]int, error) - array of stream indices in custom order (nil if no custom order), error on failure
 */
func GetChannelStreamOrder(channelName string) ([]int, error) {
	//logger.Debug("{stream/streamorder - GetChannelStreamOrder} Getting stream order for channel: %s", channelName)

	orderMutex.RLock()
	defer orderMutex.RUnlock()

	// Load current orders
	orders, err := LoadStreamOrders()
	if err != nil {
		logger.Error("{stream/streamorder - GetChannelStreamOrder} Failed to load stream orders for retrieval: %v", err)
		return nil, err
	}

	// Find matching channel order
	for _, order := range orders.ChannelOrders {
		if order.Channel == channelName {
			logger.Debug("{stream/streamorder - GetChannelStreamOrder} Found custom order for channel %s: %d streams", channelName, len(order.StreamOrder))
			return order.StreamOrder, nil
		}
	}

	// No custom order found - return nil to indicate default ordering
	//logger.Debug("{stream/streamorder - GetChannelStreamOrder} No custom order found for channel: %s, using default ordering", channelName)
	return nil, nil
}
