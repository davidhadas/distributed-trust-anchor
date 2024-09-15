package autonomous_peer

import "sync"

type Data struct {
	dataMap       map[string][]byte // map[dataType]<data bytes>
	mu            sync.Mutex
	dataUpdateMap map[string]func([]byte) // map[dataType]<func([]byte)>
}

// SetData for a given dataType
// Protected by data Mutex
func (d *Data) SetData(dataType string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.dataMap[dataType] = data
	if update, ok := d.dataUpdateMap[dataType]; ok {
		update(data)
	}
}

// GetData for a given dataType
// Protected by data Mutex
func (d *Data) GetData(dataType string) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.dataMap[dataType]
}

// RegisterDataUpdate for a given dataType
// Protected by data Mutex
func (d *Data) RegisterDataUpdate(dataType string, update func([]byte)) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.dataUpdateMap[dataType] = update
}
