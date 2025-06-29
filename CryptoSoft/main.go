package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
	"github.com/gin-gonic/gin"
)

// Структуры для ответа Binance
// https://api.binance.com/api/v3/ticker/price?symbol=BTCUSDT
type BinancePrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Структуры для ответа KuCoin
// https://api.kucoin.com/api/v1/market/orderbook/level1?symbol=BTC-USDT
type KuCoinResponse struct {
	Code string `json:"code"`
	Data struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	} `json:"data"`
}

type TransferFees struct {
	BinanceWithdraw float64 `json:"binance_withdraw"`
	KucoinWithdraw  float64 `json:"kucoin_withdraw"`
	BybitWithdraw   float64 `json:"bybit_withdraw"`
	OkxWithdraw     float64 `json:"okx_withdraw"`
	HuobiWithdraw   float64 `json:"huobi_withdraw"`
	BinanceDeposit  float64 `json:"binance_deposit"`
	KucoinDeposit   float64 `json:"kucoin_deposit"`
	BybitDeposit    float64 `json:"bybit_deposit"`
	OkxDeposit      float64 `json:"okx_deposit"`
	HuobiDeposit    float64 `json:"huobi_deposit"`
}

type Config struct {
	MinProfitUSD    float64                `json:"min_profit_usd"`
	Commission      float64                `json:"commission"`
	Tokens          []string               `json:"tokens"`
	MinTradeVolume  float64                `json:"min_trade_volume"`
	MaxTradeVolume  float64                `json:"max_trade_volume"`
	BankLimit       float64                `json:"bank_limit"`
	PollIntervalSec int                    `json:"poll_interval_sec"`
	TransferFees    map[string]TransferFees `json:"transfer_fees"`
	TransferNetworks map[string]map[string]string `json:"transfer_networks"`
	TransferRoutes  map[string][]string    `json:"transfer_routes"`
	TelegramToken   string                 `json:"telegram_token"`
	TelegramChatID  string                 `json:"telegram_chat_id"`
}

// Структуры для Bybit
// https://api.bybit.com/v5/market/tickers?category=spot&symbol=BTCUSDT
// Ответ: { "result": { "list": [ { "symbol": "BTCUSDT", "lastPrice": "..." } ] } }
type BybitTickers struct {
	Result struct {
		List []struct {
			Symbol    string `json:"symbol"`
			LastPrice string `json:"lastPrice"`
		} `json:"list"`
	} `json:"result"`
}

// Структуры для OKX
// https://www.okx.com/api/v5/market/ticker?instId=BTC-USDT
// Ответ: { "data": [ { "instId": "BTC-USDT", "last": "..." } ] }
type OKXTickers struct {
	Data []struct {
		InstId string `json:"instId"`
		Last  string `json:"last"`
	} `json:"data"`
}

// Структуры для Huobi
// https://api.huobi.pro/market/detail/merged?symbol=btcusdt
// Ответ: { "tick": { "close": ... } }
type HuobiMerged struct {
	Tick struct {
		Close float64 `json:"close"`
	} `json:"tick"`
}

type Stats struct {
	CheckedRoutes   int
	FoundArbs       int
	TotalProfit     float64
	MaxProfit       float64
}

type AccessKey struct {
	Key   string `json:"key"`
	Until string `json:"until"`
}

var exchanges = []string{"binance", "kucoin", "bybit", "okx", "huobi"}
var (
	lastPrices   = make(map[string]map[string]float64) // token -> exchange -> price
	lastArbs     = []string{}
	lastStatsObj Stats
	accessKeys   []AccessKey
)

func getBinancePrice(symbol string) (float64, error) {
	url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", symbol)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var price BinancePrice
	if err := json.Unmarshal(body, &price); err != nil {
		return 0, err
	}
	var p float64
	fmt.Sscanf(price.Price, "%f", &p)
	return p, nil
}

func getKuCoinPrice(symbol string) (float64, error) {
	url := fmt.Sprintf("https://api.kucoin.com/api/v1/market/orderbook/level1?symbol=%s", symbol)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var res KuCoinResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, err
	}
	var p float64
	fmt.Sscanf(res.Data.Price, "%f", &p)
	return p, nil
}

func loadConfig(path string) (Config, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func sendTelegram(token, chatID, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	body := fmt.Sprintf("chat_id=%s&text=%s", chatID, message)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("telegram error: %s", resp.Status)
	}
	return nil
}

func buildGuide(direction, token string, volume float64, fees TransferFees, networks map[string]map[string]string, binancePrice, kucoinPrice float64, commission float64) string {
	net := networks[token]
	var guide string
	if direction == "kucoin_to_binance" {
		guide = fmt.Sprintf(
			"ШАГИ ДЛЯ АРБИТРАЖА (%s):\n"+
			"1. Купить %s на KuCoin по цене %.2f USDT.\n"+
			"2. Перевести %s с KuCoin на Binance через сеть %s.\n"+
			"   Комиссия на вывод: %.6f %s.\n"+
			"3. Дождаться поступления средств на Binance.\n"+
			"4. Продать %s на Binance по цене %.2f USDT.\n"+
			"5. Учитывайте комиссию биржи на продажу: %.2f%%.\n",
			token, token, kucoinPrice, token, net["binance"], fees.KucoinWithdraw, token[:len(token)-4], token, binancePrice, commission,
		)
	} else {
		guide = fmt.Sprintf(
			"ШАГИ ДЛЯ АРБИТРАЖА (%s):\n"+
			"1. Купить %s на Binance по цене %.2f USDT.\n"+
			"2. Перевести %s с Binance на KuCoin через сеть %s.\n"+
			"   Комиссия на вывод: %.6f %s.\n"+
			"3. Дождаться поступления средств на KuCoin.\n"+
			"4. Продать %s на KuCoin по цене %.2f USDT.\n"+
			"5. Учитывайте комиссию биржи на продажу: %.2f%%.\n",
			token, token, binancePrice, token, net["kucoin"], fees.BinanceWithdraw, token[:len(token)-4], token, kucoinPrice, commission,
		)
	}
	guide += "\nВНИМАНИЕ: Проверьте адреса и сеть перевода перед отправкой! Ошибка может привести к потере средств."
	return guide
}

// Универсальная функция получения цены
func getPrice(exchange, symbol string) (float64, error) {
	switch exchange {
	case "binance":
		return getBinancePrice(symbol)
	case "kucoin":
		return getKuCoinPrice(symbol)
	case "bybit":
		return getBybitPrice(symbol)
	case "okx":
		return getOKXPrice(symbol)
	case "huobi":
		return getHuobiPrice(symbol)
	default:
		return 0, fmt.Errorf("unknown exchange: %s", exchange)
	}
}

func getBybitPrice(symbol string) (float64, error) {
		url := fmt.Sprintf("https://api.bybit.com/v5/market/tickers?category=spot&symbol=%s", symbol)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var res BybitTickers
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, err
	}
	if len(res.Result.List) == 0 {
		return 0, fmt.Errorf("no data from bybit")
	}
	var p float64
	fmt.Sscanf(res.Result.List[0].LastPrice, "%f", &p)
	return p, nil
}

func getOKXPrice(symbol string) (float64, error) {
	url := fmt.Sprintf("https://www.okx.com/api/v5/market/ticker?instId=%s", symbol)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var res OKXTickers
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, err
	}
	if len(res.Data) == 0 {
		return 0, fmt.Errorf("no data from okx")
	}
	var p float64
	fmt.Sscanf(res.Data[0].Last, "%f", &p)
	return p, nil
}

func getHuobiPrice(symbol string) (float64, error) {
	url := fmt.Sprintf("https://api.huobi.pro/market/detail/merged?symbol=%s", strings.ToLower(symbol))
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var res HuobiMerged
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, err
	}
	return res.Tick.Close, nil
}

func loadAccessKeys() {
	data, err := ioutil.ReadFile("access_keys.json")
	if err != nil {
		accessKeys = nil
		return
	}
	_ = json.Unmarshal(data, &accessKeys)
}

func isValidKey(key string) bool {
	for _, k := range accessKeys {
		if k.Key == key {
			t, _ := time.Parse("2006-01-02", k.Until)
			return time.Now().Before(t)
		}
	}
	return false
}

func startWebServer() {
	loadAccessKeys()
	r := gin.Default()
	r.LoadHTMLGlob("templates/*")

	r.GET("/login", func(c *gin.Context) {
		c.HTML(200, "login.tmpl", gin.H{"error": ""})
	})

	r.POST("/login", func(c *gin.Context) {
		key := c.PostForm("key")
		if isValidKey(key) {
			c.SetCookie("session_key", key, 3600*24*30, "/", "", false, true)
			c.Redirect(302, "/")
		} else {
			c.HTML(200, "login.tmpl", gin.H{"error": "Неверный или истёкший ключ"})
		}
	})

	r.GET("/logout", func(c *gin.Context) {
		c.SetCookie("session_key", "", -1, "/", "", false, true)
		c.Redirect(302, "/login")
	})

	r.GET("/", func(c *gin.Context) {
		key, err := c.Cookie("session_key")
		if err != nil || !isValidKey(key) {
			c.Redirect(302, "/login")
			return
		}
		c.HTML(200, "index.tmpl", gin.H{})
	})

	r.GET("/api/prices", func(c *gin.Context) {
		key, err := c.Cookie("session_key")
		if err != nil || !isValidKey(key) {
			c.JSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.JSON(200, lastPrices)
	})

	r.GET("/api/arbs", func(c *gin.Context) {
		key, err := c.Cookie("session_key")
		if err != nil || !isValidKey(key) {
			c.JSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.JSON(200, lastArbs)
	})

	r.GET("/api/stats", func(c *gin.Context) {
		key, err := c.Cookie("session_key")
		if err != nil || !isValidKey(key) {
			c.JSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.JSON(200, lastStatsObj)
	})

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	r.Run(":" + port)
}

func main() {
	cfg, err := loadConfig("config.json")
	if err != nil {
		fmt.Println("Не удалось загрузить config.json, используются значения по умолчанию")
		cfg.MinProfitUSD = 10
		cfg.Commission = 0.2
		cfg.Tokens = []string{"BTCUSDT"}
		cfg.MinTradeVolume = 50
		cfg.MaxTradeVolume = 10000
		cfg.BankLimit = 5000
		cfg.PollIntervalSec = 5
		cfg.TransferFees = map[string]TransferFees{}
		cfg.TransferNetworks = map[string]map[string]string{}
		cfg.TelegramToken = ""
		cfg.TelegramChatID = ""
	}
	stats := Stats{}
	lastStats := time.Now()

	go startWebServer()

	for {
		lastPrices = make(map[string]map[string]float64)
		arbs := []string{}
		for _, token := range cfg.Tokens {
			base := token[:len(token)-4]
			prices := make(map[string]float64)
			errors := make(map[string]error)
			for _, ex := range exchanges {
				var symbol string
				switch ex {
				case "binance":
					symbol = token
				case "kucoin":
					if len(token) > 4 && token[len(token)-4:] == "USDT" {
						symbol = token[:len(token)-4] + "-USDT"
					} else {
						symbol = token
					}
				case "okx":
					if len(token) > 4 && token[len(token)-4:] == "USDT" {
						symbol = token[:len(token)-4] + "-USDT"
					} else {
						symbol = token
					}
				case "bybit":
					symbol = token
				case "huobi":
					symbol = strings.ToLower(token)
				}
				price, err := getPrice(ex, symbol)
				if err == nil && price > 0 {
					prices[ex] = price
				} else {
					errors[ex] = err
				}
			}
			lastPrices[token] = prices
			fmt.Printf("\nТокен: %s\n", token)
			for _, ex := range exchanges {
				if price, ok := prices[ex]; ok {
					fmt.Printf("  %s: %.6f\n", ex, price)
				} else if err, ok := errors[ex]; ok {
					fmt.Printf("  %s: ошибка получения цены: %v\n", ex, err)
				} else {
					fmt.Printf("  %s: нет данных\n", ex)
				}
			}
			// Перебираем все пары бирж
			for _, exFrom := range exchanges {
				for _, exTo := range exchanges {
					if exFrom == exTo {
						continue
					}
					priceFrom, ok1 := prices[exFrom]
					priceTo, ok2 := prices[exTo]
					if !ok1 || !ok2 {
						continue
					}
					routes, ok := cfg.TransferRoutes[base]
					if !ok {
						routes = []string{base}
					}
					for _, routeToken := range routes {
						fees, ok := cfg.TransferFees[routeToken]
						if !ok {
							fmt.Printf("  [%s->%s через %s] Нет данных о комиссиях\n", exFrom, exTo, routeToken)
							continue
						}
						maxVolume := cfg.BankLimit / priceFrom
						if v := cfg.BankLimit / priceTo; v < maxVolume {
							maxVolume = v
						}
						if maxVolume > cfg.MaxTradeVolume {
							maxVolume = cfg.MaxTradeVolume
						}
						if maxVolume < cfg.MinTradeVolume {
							fmt.Printf("  [%s->%s через %s] Недостаточный объем для сделки (%.4f)\n", exFrom, exTo, routeToken, maxVolume)
							continue
						}
						withdrawFee := 0.0
						depositFee := 0.0
						switch exFrom {
						case "binance":
							withdrawFee = fees.BinanceWithdraw
						case "kucoin":
							withdrawFee = fees.KucoinWithdraw
						case "bybit":
							withdrawFee = fees.BybitWithdraw
						case "okx":
							withdrawFee = fees.OkxWithdraw
						case "huobi":
							withdrawFee = fees.HuobiWithdraw
						}
						switch exTo {
						case "binance":
							depositFee = fees.BinanceDeposit
						case "kucoin":
							depositFee = fees.KucoinDeposit
						case "bybit":
							depositFee = fees.BybitDeposit
						case "okx":
							depositFee = fees.OkxDeposit
						case "huobi":
							depositFee = fees.HuobiDeposit
						}
						volAfterWithdraw := maxVolume - withdrawFee
						if volAfterWithdraw <= 0 {
							fmt.Printf("  [%s->%s через %s] После вывода ничего не осталось (%.4f)\n", exFrom, exTo, routeToken, volAfterWithdraw)
							continue
						}
						finalVolume := volAfterWithdraw
						if routeToken != base {
							finalVolume = (maxVolume * priceFrom / priceTo) - withdrawFee
							if finalVolume <= 0 {
								fmt.Printf("  [%s->%s через %s] После обмена ничего не осталось (%.4f)\n", exFrom, exTo, routeToken, finalVolume)
								continue
							}
						}
						usdAfterSell := finalVolume * priceTo * (1 - cfg.Commission/100)
						usdSpent := maxVolume * priceFrom + depositFee * priceTo
						profit := usdAfterSell - usdSpent
						stats.CheckedRoutes++
						if profit > cfg.MinProfitUSD {
							stats.FoundArbs++
							stats.TotalProfit += profit
							if profit > stats.MaxProfit {
								stats.MaxProfit = profit
							}
							netFrom := cfg.TransferNetworks[routeToken][exFrom]
							netTo := cfg.TransferNetworks[routeToken][exTo]
							msg := fmt.Sprintf("Арбитраж: %s -> %s через %s. %s Объем: %.4f, Прибыль: %.2f USD (учтены комиссии)", exFrom, exTo, routeToken, token, maxVolume, profit)
							arbs = append(arbs, msg)
							fmt.Println(msg)
							if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
								guide := fmt.Sprintf(
									"ШАГИ:\n1. Купить %s на %s по цене %.2f USDT.\n2. Перевести %s с %s на %s через сеть %s->%s.\n   Комиссия на вывод: %.6f %s.\n3. Дождаться поступления.\n4. Продать %s на %s по цене %.2f USDT.\n5. Учитывайте комиссию биржи: %.2f%%.\nВНИМАНИЕ: Проверьте адреса и сеть перевода!",
									token, exFrom, priceFrom, routeToken, exFrom, exTo, netFrom, netTo, withdrawFee, routeToken, token, exTo, priceTo, cfg.Commission)
								err := sendTelegram(cfg.TelegramToken, cfg.TelegramChatID, msg+"\n"+guide)
								if err != nil {
									fmt.Println("Ошибка отправки в Telegram:", err)
								}
							}
						} else {
							fmt.Printf("  [%s->%s через %s] Недостаточная прибыль: %.2f USD\n", exFrom, exTo, routeToken, profit)
						}
					}
				}
			}
		}
		lastArbs = arbs
		lastStatsObj = stats
		if time.Since(lastStats) > 60*time.Second {
			fmt.Printf("\nСТАТИСТИКА ЗА СЕССИЮ:\n  Проверено маршрутов: %d\n  Найдено арбитражей: %d\n  Суммарная потенциальная прибыль: %.2f USD\n  Максимальная прибыль за сделку: %.2f USD\n\n", stats.CheckedRoutes, stats.FoundArbs, stats.TotalProfit, stats.MaxProfit)
			lastStats = time.Now()
		}
		time.Sleep(time.Duration(cfg.PollIntervalSec) * time.Second)
	}
}
