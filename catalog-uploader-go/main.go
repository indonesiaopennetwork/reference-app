package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nThis tool uploads a catalog from a pipe-delimited CSV file to the BPP.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  --file                 (Required) Path to the CSV file containing the products. The file must use '|' as the delimiter.\n")
		fmt.Fprintf(os.Stderr, "  --catalog-id           (Required) A unique identifier for this catalog (e.g., cat-go-001).\n")
		fmt.Fprintf(os.Stderr, "  --catalog-name         (Required) A human-readable name for the catalog (e.g., \"Go Test Catalog\").\n")
		fmt.Fprintf(os.Stderr, "  --provider-id          (Required) A unique identifier for the provider offering these products (e.g., provider-go-001).\n")
		fmt.Fprintf(os.Stderr, "  --provider-name        (Required) The name of the provider (e.g., \"Go Test Provider\").\n")
		fmt.Fprintf(os.Stderr, "  --catalog-short-desc   (Optional) A short description of the catalog.\n")
		fmt.Fprintf(os.Stderr, "  --catalog-long-desc    (Optional) A detailed description of the catalog.\n")
		fmt.Fprintf(os.Stderr, "  --bpp-url              (Optional) The base URL of the BPP to upload to (default: http://localhost:8080).\n\n")
		fmt.Fprintf(os.Stderr, "Example:\n")
		fmt.Fprintf(os.Stderr, "  ./catalog-uploader --file \"products.csv\" --catalog-id \"cat-01\" --catalog-name \"My Catalog\" --provider-id \"prov-01\" --provider-name \"My Store\"\n")
	}

	filePath := flag.String("file", "", "Path to the CSV file")
	catalogID := flag.String("catalog-id", "", "Catalog ID")
	catalogName := flag.String("catalog-name", "", "Catalog Name")
	catalogShortDesc := flag.String("catalog-short-desc", "", "Catalog Short Description")
	catalogLongDesc := flag.String("catalog-long-desc", "", "Catalog Long Description")
	providerID := flag.String("provider-id", "", "Provider ID")
	providerName := flag.String("provider-name", "", "Provider Name")
	bppURL := flag.String("bpp-url", "http://localhost:8080", "BPP URL")
	flag.Parse()

	if *filePath == "" || *catalogID == "" || *catalogName == "" || *providerID == "" || *providerName == "" {
		fmt.Println("Error: --file, --catalog-id, --catalog-name, --provider-id, and --provider-name are required flags.")
		flag.Usage()
		os.Exit(1)
	}

	file, err := os.Open(*filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = '|'
	
	// Read header
	headers, err := reader.Read()
	if err != nil {
		fmt.Printf("Error reading CSV header: %v\n", err)
		os.Exit(1)
	}

	headerMap := make(map[string]int)
	for i, h := range headers {
		headerMap[strings.TrimSpace(h)] = i
	}

	var resources []map[string]any
	var offers []map[string]any

	startDate := time.Now().UTC().Format(time.RFC3339)
	endDate := time.Now().UTC().Add(365 * 24 * time.Hour).Format(time.RFC3339)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Error reading CSV row: %v\n", err)
			continue
		}

		getValue := func(col string) string {
			idx, ok := headerMap[col]
			if ok && idx < len(record) {
				return record[idx]
			}
			return ""
		}

		resourceID := getValue("resource_id")
		offerID := getValue("offer_id")
		nameBahasa := getValue("name_bahasa")
		nameEnglish := getValue("name_english")
		
		fullName := nameBahasa
		if nameEnglish != "" && !strings.Contains(nameBahasa, nameEnglish) {
			fullName = fmt.Sprintf("%s (%s)", nameBahasa, nameEnglish)
		}

		weightG, _ := strconv.Atoi(getValue("weight_g"))
		netQty, _ := strconv.Atoi(getValue("net_qty_value"))
		price, _ := strconv.Atoi(getValue("price_idr"))

		// Construct Resource
		resource := map[string]any{
			"id": resourceID,
			"descriptor": map[string]any{
				"name":      fullName,
				"shortDesc": getValue("short_desc"),
				"longDesc":  getValue("long_desc"),
			},
			"resourceAttributes": map[string]any{
				"@type":    "ion:IONTradeResource",
				"@context": "https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld",
				"identity": map[string]any{
					"brand":         getValue("brand"),
					"originCountry": getValue("origin_country"),
				},
				"physical": map[string]any{
					"weight": map[string]any{
						"unitCode":     "G",
						"unitQuantity": weightG,
					},
					"appearance": map[string]any{
						"color":    getValue("color"),
						"finish":   getValue("finish"),
						"material": getValue("material"),
					},
				},
				"packagedGoodsDeclaration": map[string]any{
					"netQuantity": map[string]any{
						"unitCode":     func() string { if val := getValue("net_qty_unit"); val != "" { return val } else { return "G" } }(),
						"unitQuantity": netQty,
					},
					"commonOrGenericName": getValue("common_name"),
					"manufacturerOrPacker": map[string]any{
						"name":    getValue("mfr_name"),
						"type":    "MANUFACTURER",
						"address": getValue("mfr_address"),
					},
				},
			},
			"provider": map[string]any{
				"id": *providerID,
				"descriptor": map[string]any{
					"name": *providerName,
				},
			},
		}

		imageURL := getValue("image_url")
		if imageURL != "" {
			label := nameEnglish
			if label == "" {
				label = "Image"
			}
			resource["descriptor"].(map[string]any)["mediaFile"] = []map[string]any{
				{
					"uri":      imageURL,
					"mimeType": "image/jpeg",
					"label":    label,
				},
			}
		}

		resources = append(resources, resource)

		// Construct Offer
		currency := getValue("currency")
		if currency == "" {
			currency = "IDR"
		}

		offer := map[string]any{
			"id": offerID,
			"descriptor": map[string]any{
				"name": fullName,
			},
			"provider": map[string]any{
				"id": *providerID,
				"descriptor": map[string]any{
					"name": *providerName,
				},
			},
			"resourceIds": []string{resourceID},
			"considerations": []map[string]any{
				{
					"id": fmt.Sprintf("%s-price", offerID),
					"status": map[string]any{
						"name": "ACTIVE",
						"code": "ACTIVE",
					},
					"considerationAttributes": map[string]any{
						"@type":       "ion:IONTradeConsideration",
						"@context":    "https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld",
						"currency":    currency,
						"totalAmount": price,
						"breakup": []map[string]any{
							{
								"type":   "BASE_PRICE",
								"title":  fullName,
								"amount": price,
							},
						},
						"paymentMethods": []string{"PREPAID", "COD"},
					},
				},
			},
			"validity": map[string]any{
				"startDate": startDate,
				"endDate":   endDate,
			},
			"offerAttributes": map[string]any{
				"@type":    "ion:IONTradeOffer",
				"@context": "https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld",
				"policies": map[string]any{
					"returns": map[string]any{
						"allowed": false,
					},
					"replacement": map[string]any{
						"allowed": false,
					},
					"cancellation": map[string]any{
						"window":      "PT4H",
						"allowed":     true,
						"cutoffEvent": "BEFORE_PACKING",
					},
				},
				"serviceability": map[string]any{
					"timing": []map[string]any{
						{
							"timeRange": map[string]any{
								"end":   "18:00",
								"start": "09:00",
							},
							"daysOfWeek": []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
						},
					},
					"distanceConstraint": map[string]any{
						"unit":        "KM",
						"maxDistance": 10,
					},
				},
				"paymentConstraints": map[string]any{
					"codAvailable": true,
				},
			},
		}

		offers = append(offers, offer)
	}

	payload := map[string]any{
		"context": map[string]any{
			"action": "on_discover",
		},
		"message": map[string]any{
			"catalogs": []map[string]any{
				{
					"id": *catalogID,
					"descriptor": map[string]any{
						"name":      *catalogName,
						"shortDesc": *catalogShortDesc,
						"longDesc":  *catalogLongDesc,
					},
					"provider": map[string]any{
						"id": *providerID,
						"descriptor": map[string]any{
							"name": *providerName,
						},
					},
					"resources": resources,
					"offers":    offers,
				},
			},
		},
	}

	url := fmt.Sprintf("%s/catalog/publish", strings.TrimRight(*bppURL, "/"))

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Error marshaling JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Publishing catalog '%s' with %d resources to %s...\n", *catalogName, len(resources), url)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error making HTTP request: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response body: %v\n", err)
		os.Exit(1)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Printf("Success! Response Code: %d\n", resp.StatusCode)
		fmt.Printf("Response Body: %s\n", string(bodyBytes))
	} else {
		fmt.Printf("HTTP Error: %d - %s\n", resp.StatusCode, resp.Status)
		fmt.Printf("Response Body: %s\n", string(bodyBytes))
		os.Exit(1)
	}
}
