package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Instance struct {
	InstanceType   string `json:"InstanceType"`
	VCPUS          int    `json:"VCPUS"`
	Memory         string `json:"Memory"`
	SpotSavingRate string `json:"SpotSavingRate"`
	SpotPrice      string `json:"SpotPrice"`
}

type Response struct {
	Prices []Instance `json:"Prices"`
}

type Region struct {
	Name      string `json:"name"`
	Code      string `json:"code"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	Continent string `json:"continent"`
}

type GlobalDeal struct {
	InstanceType string  `json:"instanceType"`
	VCPUS        int     `json:"cpus"`
	Memory       string  `json:"memory"`
	SpotPrice    float64 `json:"price"`
	PricePerVCPU float64 `json:"pricePerVCPU"`
	Region       string  `json:"region"`
}

func main() {
	http.HandleFunc("/api/regions", getRegions)
	http.HandleFunc("/api/spot-deals", getSpotDealsHandler)
	http.HandleFunc("/api/best-global-deal", getBestGlobalDeal)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	fmt.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := fetchRegions()
	if err != nil {
		log.Printf("Error fetching regions: %v", err)
		http.Error(w, "Failed to fetch regions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(regions)
}

func getSpotDealsHandler(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		http.Error(w, "Region parameter is required", http.StatusBadRequest)
		return
	}

	deals, err := getSpotDeals(region)
	if err != nil {
		log.Printf("Error getting spot deals: %v", err)
		http.Error(w, "Failed to get spot deals", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deals)
}

func getSpotDeals(region string) ([]Instance, error) {
	url := fmt.Sprintf("https://ec2.shop?region=%s&filter=ebs,cpu>=4,cpu<=32", region)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response Response
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	var highSavingsInstances []Instance
	for _, instance := range response.Prices {
		savingsRate, err := strconv.Atoi(strings.TrimSuffix(instance.SpotSavingRate, "%"))
		if err == nil && savingsRate > 50 {
			highSavingsInstances = append(highSavingsInstances, instance)
		}
	}

	sort.Slice(highSavingsInstances, func(i, j int) bool {
		priceI, _ := strconv.ParseFloat(highSavingsInstances[i].SpotPrice, 64)
		priceJ, _ := strconv.ParseFloat(highSavingsInstances[j].SpotPrice, 64)
		ratioI := priceI / float64(highSavingsInstances[i].VCPUS)
		ratioJ := priceJ / float64(highSavingsInstances[j].VCPUS)
		return ratioI < ratioJ
	})

	return highSavingsInstances, nil
}

func getBestGlobalDeal(w http.ResponseWriter, r *http.Request) {
	regions, err := fetchRegions()
	if err != nil {
		log.Printf("Error fetching regions: %v", err)
		http.Error(w, "Failed to fetch regions", http.StatusInternalServerError)
		return
	}

	var wg sync.WaitGroup
	dealsChan := make(chan GlobalDeal, len(regions))

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			deals, err := getSpotDeals(r)
			if err != nil {
				log.Printf("Error getting spot deals for region %s: %v", r, err)
				return
			}
			if len(deals) > 0 {
				price, _ := strconv.ParseFloat(deals[0].SpotPrice, 64)
				pricePerVCPU := price / float64(deals[0].VCPUS)
				dealsChan <- GlobalDeal{
					InstanceType: deals[0].InstanceType,
					VCPUS:        deals[0].VCPUS,
					Memory:       deals[0].Memory,
					SpotPrice:    price,
					PricePerVCPU: pricePerVCPU,
					Region:       r,
				}
			}
		}(region)
	}

	wg.Wait()
	close(dealsChan)

	var bestDeals []GlobalDeal
	for deal := range dealsChan {
		bestDeals = append(bestDeals, deal)
	}

	if len(bestDeals) == 0 {
		http.Error(w, "No deals found", http.StatusNotFound)
		return
	}

	sort.Slice(bestDeals, func(i, j int) bool {
		return bestDeals[i].PricePerVCPU < bestDeals[j].PricePerVCPU
	})

	// Return top 5 deals or all if less than 5
	numDeals := 5
	if len(bestDeals) < 5 {
		numDeals = len(bestDeals)
	}
	topDeals := bestDeals[:numDeals]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(topDeals)
}

func fetchRegions() ([]string, error) {
	resp, err := http.Get("https://b0.p.awsstatic.com/locations/1.0/aws/current/locations.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var regions map[string]Region
	err = json.Unmarshal(body, &regions)
	if err != nil {
		return nil, err
	}

	var regionCodes []string
	for _, region := range regions {
		if region.Type == "AWS Region" {
			regionCodes = append(regionCodes, region.Code)
		}
	}

	sort.Strings(regionCodes)  // Sort the region codes alphabetically

	return regionCodes, nil
}
