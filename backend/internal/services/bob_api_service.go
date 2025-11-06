package services

import (
	"bob-hackathon/internal/config"
	"bob-hackathon/internal/models"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type BOBAPIService struct {
	baseURL       string
	cache         []models.Vehicle
	lastFetch     time.Time
	cacheDuration time.Duration
	httpClient    *http.Client
	mu            sync.RWMutex
}

var bobAPIServiceInstance *BOBAPIService
var bobAPIServiceOnce sync.Once

func GetBOBAPIService() *BOBAPIService {
	bobAPIServiceOnce.Do(func() {
		bobAPIServiceInstance = &BOBAPIService{
			baseURL:       config.AppConfig.BOBAPIBaseURL,
			cache:         []models.Vehicle{},
			cacheDuration: 5 * time.Minute,
			httpClient: &http.Client{
				Timeout: 10 * time.Second,
			},
		}
	})
	return bobAPIServiceInstance
}

func (b *BOBAPIService) GetSublots(forceRefresh bool) ([]models.Vehicle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Verificar cache
	if !forceRefresh && time.Since(b.lastFetch) < b.cacheDuration && len(b.cache) > 0 {
		log.Printf("Usando cache de vehículos (%d items)", len(b.cache))
		return b.cache, nil
	}

	// Fetch desde API
	url := fmt.Sprintf("%s/sublots/details", b.baseURL)
	resp, err := b.httpClient.Get(url)
	if err != nil {
		log.Printf("Error obteniendo vehiculos de BOB API: %v", err)
		return nil, fmt.Errorf("error al obtener sublots: %w", err)
	}
	defer resp.Body.Close()

	// Verificar status code
	if resp.StatusCode != http.StatusOK {
		log.Printf("BOB API devolvio status %d", resp.StatusCode)
		return nil, fmt.Errorf("bob api devolvio status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error al leer respuesta: %w", err)
	}

	var apiResponse struct {
		Data []struct {
			ID           string  `json:"id"`
			Brand        string  `json:"brand"`
			Model        string  `json:"model"`
			Year         string  `json:"year"`
			StartPrice   float64 `json:"start_price"`
			AuctionType  string  `json:"auction_type"`
			Status       string  `json:"status"`
			Image        string  `json:"image"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("error al parsear respuesta: %w", err)
	}

	// Convertir a nuestro modelo
	vehicles := make([]models.Vehicle, 0, len(apiResponse.Data))
	for _, item := range apiResponse.Data {
		vehicle := models.Vehicle{
			ID:           item.ID,
			Marca:        item.Brand,
			Modelo:       item.Model,
			Ano:          item.Year,
			PrecioInicio: item.StartPrice,
			TipoSubasta:  item.AuctionType,
			Estado:       item.Status,
			Imagen:       item.Image,
		}
		vehicles = append(vehicles, vehicle)
	}

	b.cache = vehicles
	b.lastFetch = time.Now()

	log.Printf("%d vehículos obtenidos de la API BOB", len(vehicles))
	return vehicles, nil
}

func (b *BOBAPIService) SearchVehicles(marca, modelo string, precioMin, precioMax float64, tipoSubasta string, limit int) ([]models.Vehicle, error) {
	vehicles, err := b.GetSublots(false)
	if err != nil {
		return nil, err
	}

	var results []models.Vehicle

	for _, v := range vehicles {
		// Filtrar por marca
		if marca != "" && !strings.EqualFold(v.Marca, marca) {
			continue
		}

		// Filtrar por modelo
		if modelo != "" && !strings.Contains(strings.ToLower(v.Modelo), strings.ToLower(modelo)) {
			continue
		}

		// Filtrar por precio mínimo
		if precioMin > 0 && v.PrecioInicio < precioMin {
			continue
		}

		// Filtrar por precio máximo
		if precioMax > 0 && v.PrecioInicio > precioMax {
			continue
		}

		// Filtrar por tipo de subasta
		if tipoSubasta != "" && !strings.EqualFold(v.TipoSubasta, tipoSubasta) {
			continue
		}

		results = append(results, v)

		// Limitar resultados
		if limit > 0 && len(results) >= limit {
			break
		}
	}

	return results, nil
}

func (b *BOBAPIService) GetVehicleByID(id string) (*models.Vehicle, error) {
	vehicles, err := b.GetSublots(false)
	if err != nil {
		return nil, err
	}

	for _, v := range vehicles {
		if v.ID == id {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("vehículo no encontrado")
}

func (b *BOBAPIService) GetVehiclesContext(limit int) string {
	vehicles, err := b.GetSublots(false)
	if err != nil {
		return ""
	}

	var context strings.Builder
	context.WriteString("Vehículos disponibles en subasta:\n\n")

	count := 0
	for _, v := range vehicles {
		if count >= limit {
			break
		}

		context.WriteString(fmt.Sprintf("- %s %s %s - Precio inicial: $%.2f - Tipo: %s\n",
			v.Marca, v.Modelo, v.Ano, v.PrecioInicio, v.TipoSubasta))

		count++
	}

	return context.String()
}
