package streamorder

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

type ChannelStreamOrder struct {
	Channel     string `json:"channel"`
	StreamOrder []int  `json:"streamOrder"` // Array of stream indexes in custom order
}

type StreamOrderFile struct {
	ChannelOrders []ChannelStreamOrder `json:"channelOrders"`
}

var (
	orderMutex sync.RWMutex
)

func LoadStreamOrders() (*StreamOrderFile, error) {
	orderPath := "/settings/stream-orders.json"

	if _, err := os.Stat(orderPath); os.IsNotExist(err) {
		return &StreamOrderFile{ChannelOrders: []ChannelStreamOrder{}}, nil
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

func SaveStreamOrders(orders *StreamOrderFile) error {
	orderPath := "/settings/stream-orders.json"

	data, err := json.MarshalIndent(orders, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal stream orders: %w", err)
	}

	return os.WriteFile(orderPath, data, 0644)
}

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
