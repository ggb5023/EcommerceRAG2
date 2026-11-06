// Package commercesim serves the synthetic commerce-lab-v1 dataset through the
// versioned facts contract and a small read-only development REST surface.
// It is a development double: loopback only, never part of selfhost.
package commercesim

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Manifest is the dataset manifest. Hashes are verified at load so a partial
// copy fails loudly instead of serving half a shop.
type Manifest struct {
	SchemaVersion      int            `yaml:"schema_version"`
	DatasetID          string         `yaml:"dataset_id"`
	Seed               int            `yaml:"seed"`
	ReferenceDate      string         `yaml:"reference_date"`
	FactsVersion       string         `yaml:"facts_version"`
	TenantID           string         `yaml:"tenant_id"`
	ShopID             string         `yaml:"shop_id"`
	Currency           string         `yaml:"currency"`
	FeaturedComparison []string       `yaml:"featured_comparison"`
	Files              []ManifestFile `yaml:"files"`
}

type ManifestFile struct {
	Path   string `yaml:"path"`
	Rows   int    `yaml:"rows"`
	SHA256 string `yaml:"sha256"`
}

type Product struct {
	SKU        string
	Title      string
	Category   string
	Color      string
	SizeLabel  string
	SizeChart  string
	Material   string
	CareCode   string
	PriceCents int
	Currency   string
	Attributes map[string]any
	LaunchedOn string
}

type Inventory struct {
	SKU       string
	OnHand    int
	Reserved  int
	Warehouse string
	UpdatedAt string
}

type Order struct {
	ID          string
	ShopID      string
	Status      string
	CustomerRef string
	PlacedAt    string
	UpdatedAt   string
	Currency    string
}

type OrderItem struct {
	OrderID  string
	LineNo   int
	SKU      string
	Quantity int
}

type Shipment struct {
	ID          string
	OrderID     string
	Carrier     string
	TrackingNo  string
	Status      string
	ShippedAt   string
	DeliveredAt string
}

type ShipmentEvent struct {
	ID         string
	ShipmentID string
	Seq        int
	EventAt    string
	Location   string
	Status     string
}

type Return struct {
	ID          string
	OrderID     string
	SKU         string
	ReasonCode  string
	Status      string
	RequestedAt string
	ResolvedAt  string
}

type Ticket struct {
	ID         string
	OrderID    string
	ReasonCode string
	Status     string
	CreatedAt  string
	ClosedAt   string
}

// Dataset is the parsed, cross-checked in-memory shop.
type Dataset struct {
	Manifest         Manifest
	Products         map[string]Product
	ProductOrder     []string
	Inventory        map[string]Inventory
	Orders           map[string]Order
	OrderOrder       []string
	ItemsByOrder     map[string][]OrderItem
	ShipmentsByOrder map[string][]Shipment
	EventsByShipment map[string][]ShipmentEvent
	Returns          []Return
	Tickets          []Ticket
}

// Load reads the package, verifies every listed sha256, and cross-checks the
// references the facts tools rely on.
func Load(dir string) (*Dataset, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err = yaml.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.DatasetID == "" || manifest.TenantID == "" || manifest.ShopID == "" {
		return nil, fmt.Errorf("manifest is missing dataset identity")
	}
	if manifest.FactsVersion == "" {
		manifest.FactsVersion = "v1"
	}
	for _, entry := range manifest.Files {
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Path))
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Path, readErr)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, fmt.Errorf("dataset file %s does not match its manifest hash", entry.Path)
		}
	}
	dataset := &Dataset{
		Manifest:         manifest,
		Products:         map[string]Product{},
		Inventory:        map[string]Inventory{},
		Orders:           map[string]Order{},
		ItemsByOrder:     map[string][]OrderItem{},
		ShipmentsByOrder: map[string][]Shipment{},
		EventsByShipment: map[string][]ShipmentEvent{},
	}
	if err = dataset.loadProducts(dir); err != nil {
		return nil, err
	}
	if err = dataset.loadInventory(dir); err != nil {
		return nil, err
	}
	if err = dataset.loadOrders(dir); err != nil {
		return nil, err
	}
	if err = dataset.loadShipments(dir); err != nil {
		return nil, err
	}
	if err = dataset.loadReturns(dir); err != nil {
		return nil, err
	}
	if err = dataset.loadTickets(dir); err != nil {
		return nil, err
	}
	if err = dataset.validate(); err != nil {
		return nil, err
	}
	return dataset, nil
}

func readRows(dir, name string) ([][]string, error) {
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s is empty", name)
	}
	return rows[1:], nil
}

func number(name, field, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %s is not an integer", name, field)
	}
	return parsed, nil
}

func (d *Dataset) loadProducts(dir string) error {
	rows, err := readRows(dir, "products.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 12 {
			return fmt.Errorf("products.csv: short row")
		}
		price, err := number("products.csv", "price_cents", row[8])
		if err != nil {
			return err
		}
		attributes := map[string]any{}
		if err = json.Unmarshal([]byte(row[10]), &attributes); err != nil {
			return fmt.Errorf("products.csv: attributes_json is invalid for %s", row[0])
		}
		d.Products[row[0]] = Product{
			SKU: row[0], Title: row[1], Category: row[2], Color: row[3],
			SizeLabel: row[4], SizeChart: row[5], Material: row[6], CareCode: row[7],
			PriceCents: price, Currency: row[9], Attributes: attributes, LaunchedOn: row[11],
		}
		d.ProductOrder = append(d.ProductOrder, row[0])
	}
	return nil
}

func (d *Dataset) loadInventory(dir string) error {
	rows, err := readRows(dir, "inventory.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 5 {
			return fmt.Errorf("inventory.csv: short row")
		}
		onHand, err := number("inventory.csv", "on_hand", row[1])
		if err != nil {
			return err
		}
		reserved, err := number("inventory.csv", "reserved", row[2])
		if err != nil {
			return err
		}
		d.Inventory[row[0]] = Inventory{SKU: row[0], OnHand: onHand, Reserved: reserved,
			Warehouse: row[3], UpdatedAt: row[4]}
	}
	return nil
}

func (d *Dataset) loadOrders(dir string) error {
	rows, err := readRows(dir, "orders.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 7 {
			return fmt.Errorf("orders.csv: short row")
		}
		d.Orders[row[0]] = Order{ID: row[0], ShopID: row[1], Status: row[2],
			CustomerRef: row[3], PlacedAt: row[4], UpdatedAt: row[5], Currency: row[6]}
		d.OrderOrder = append(d.OrderOrder, row[0])
	}
	items, err := readRows(dir, "order-items.csv")
	if err != nil {
		return err
	}
	for _, row := range items {
		if len(row) < 4 {
			return fmt.Errorf("order-items.csv: short row")
		}
		line, err := number("order-items.csv", "line_no", row[1])
		if err != nil {
			return err
		}
		quantity, err := number("order-items.csv", "quantity", row[3])
		if err != nil {
			return err
		}
		d.ItemsByOrder[row[0]] = append(d.ItemsByOrder[row[0]],
			OrderItem{OrderID: row[0], LineNo: line, SKU: row[2], Quantity: quantity})
	}
	return nil
}

func (d *Dataset) loadShipments(dir string) error {
	rows, err := readRows(dir, "shipments.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 7 {
			return fmt.Errorf("shipments.csv: short row")
		}
		d.ShipmentsByOrder[row[1]] = append(d.ShipmentsByOrder[row[1]],
			Shipment{ID: row[0], OrderID: row[1], Carrier: row[2], TrackingNo: row[3],
				Status: row[4], ShippedAt: row[5], DeliveredAt: row[6]})
	}
	events, err := readRows(dir, "shipment-events.csv")
	if err != nil {
		return err
	}
	for _, row := range events {
		if len(row) < 6 {
			return fmt.Errorf("shipment-events.csv: short row")
		}
		seq, err := number("shipment-events.csv", "seq", row[2])
		if err != nil {
			return err
		}
		d.EventsByShipment[row[1]] = append(d.EventsByShipment[row[1]],
			ShipmentEvent{ID: row[0], ShipmentID: row[1], Seq: seq, EventAt: row[3],
				Location: row[4], Status: row[5]})
	}
	return nil
}

func (d *Dataset) loadReturns(dir string) error {
	rows, err := readRows(dir, "returns.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 7 {
			return fmt.Errorf("returns.csv: short row")
		}
		d.Returns = append(d.Returns, Return{ID: row[0], OrderID: row[1], SKU: row[2],
			ReasonCode: row[3], Status: row[4], RequestedAt: row[5], ResolvedAt: row[6]})
	}
	return nil
}

func (d *Dataset) loadTickets(dir string) error {
	rows, err := readRows(dir, "tickets.csv")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if len(row) < 6 {
			return fmt.Errorf("tickets.csv: short row")
		}
		d.Tickets = append(d.Tickets, Ticket{ID: row[0], OrderID: row[1], ReasonCode: row[2],
			Status: row[3], CreatedAt: row[4], ClosedAt: row[5]})
	}
	return nil
}

func (d *Dataset) validate() error {
	for sku := range d.Inventory {
		if _, ok := d.Products[sku]; !ok {
			return fmt.Errorf("inventory references unknown sku %s", sku)
		}
	}
	for orderID, items := range d.ItemsByOrder {
		if _, ok := d.Orders[orderID]; !ok {
			return fmt.Errorf("order items reference unknown order %s", orderID)
		}
		for _, item := range items {
			if _, ok := d.Products[item.SKU]; !ok {
				return fmt.Errorf("order %s references unknown sku %s", orderID, item.SKU)
			}
		}
	}
	for orderID := range d.ShipmentsByOrder {
		if _, ok := d.Orders[orderID]; !ok {
			return fmt.Errorf("shipment references unknown order %s", orderID)
		}
	}
	for _, order := range d.Orders {
		if order.ShopID != d.Manifest.ShopID {
			return fmt.Errorf("order %s is outside the dataset shop", order.ID)
		}
	}
	return nil
}
