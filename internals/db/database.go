package db

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// FavoriteDevice represents a favorite USB device
type FavoriteDevice struct {
	ID          int    `json:"id"`
	VendorID    string `json:"vendorId"`
	ProductID   string `json:"productId"`
	Description string `json:"description"`
}

// InitDB initializes the SQLite database
func InitDB() error {
	// Create data directory if it doesn't exist
	dataDir := "./data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		err := os.MkdirAll(dataDir, 0755)
		if err != nil {
			return err
		}
	}

	dbPath := filepath.Join(dataDir, "favorites.db")
	var err error
	DB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}

	// Create favorites table if it doesn't exist
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS favorites (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		vendor_id TEXT NOT NULL,
		product_id TEXT NOT NULL,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(vendor_id, product_id)
	);
	`

	_, err = DB.Exec(createTableSQL)
	if err != nil {
		return err
	}

	log.Println("Database initialized successfully")
	return nil
}

// GetAllFavorites returns all favorite devices
func GetAllFavorites() ([]FavoriteDevice, error) {
	rows, err := DB.Query("SELECT id, vendor_id, product_id, description FROM favorites ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var favorites []FavoriteDevice
	for rows.Next() {
		var fav FavoriteDevice
		err := rows.Scan(&fav.ID, &fav.VendorID, &fav.ProductID, &fav.Description)
		if err != nil {
			return nil, err
		}
		favorites = append(favorites, fav)
	}

	return favorites, rows.Err()
}

// AddFavorite adds a device to favorites
func AddFavorite(vendorID, productID, description string) error {
	_, err := DB.Exec(
		"INSERT OR REPLACE INTO favorites (vendor_id, product_id, description) VALUES (?, ?, ?)",
		vendorID, productID, description,
	)
	return err
}

// RemoveFavorite removes a device from favorites
func RemoveFavorite(vendorID, productID string) error {
	_, err := DB.Exec(
		"DELETE FROM favorites WHERE vendor_id = ? AND product_id = ?",
		vendorID, productID,
	)
	return err
}

// IsFavorite checks if a device is in favorites
func IsFavorite(vendorID, productID string) (bool, error) {
	var count int
	err := DB.QueryRow(
		"SELECT COUNT(*) FROM favorites WHERE vendor_id = ? AND product_id = ?",
		vendorID, productID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

