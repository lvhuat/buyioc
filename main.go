package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lvhuat/textformatter"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v2"
)

var (
	log = logrus.WithFields(logrus.Fields{})
)

var gridFile = flag.String("grid", "grid.csv", "网格文件")
var cfgFile = flag.String("cfg", "config.json", "基本配置文件")
var testMode = flag.Bool("test", false, "仅打印不会下单，不会执行网格")
var mf = flag.Bool("mf", false, "仅监控保证金率")

type EventRejectOrder struct {
	ClientId string
}

type GridPersistItem struct {
	Time     time.Time
	SpotName string

	FutureName string
	Grids      []*TradeGrid
}

func persistGrids() {
	d, err := yaml.Marshal(&GridPersistItem{
		Grids:      grids,
		Time:       time.Now(),
		SpotName:   spotName,
		FutureName: spotName,
	})
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	ioutil.WriteFile("save.yaml", d, 0666)
}

func BalanceOfCoin(balances []*Balance, coin string) *Balance {
	for _, balance := range balances {
		if balance.Coin == coin {
			return balance
		}
	}

	return &Balance{
		Coin: coin,
	}
}

func main() {
	logrus.SetFormatter(&textformatter.TextFormatter{})

	flag.Parse()

	config := loadBaseConfigAndAssign(*cfgFile)

	for i := 3; i > 0; i-- {
		log.Infoln("Counting ", i)
		time.Sleep(time.Second)
	}

	go func() {
		for {
			wsclient := WebsocketClient{
				apiKey:     apiKey,
				secret:     []byte(secretKey),
				subAccount: subAccount,
				quit:       make(chan interface{}),
			}

			if err := wsclient.dial(false); err != nil {
				logrus.WithError(err).Errorln("DialWebsocketFailed")
				time.Sleep(time.Second)
				continue
			}

			wsclient.ping()
			wsclient.login()
			wsclient.subOrder()
			wsclient.onOrderChange = func(body []byte) {
				order := &Order{}
				raw := gjson.GetBytes(body, "data").Raw
				json.Unmarshal([]byte(raw), &order)
				if order.FilledSize > 0 {
					SendDingtalkText(config.Ding, fmt.Sprintf("BuyIOC %s filled qty=%v avgPrice=%v", config.Symbol, order.FilledSize, order.AvgFillPrice))
				}
			}

			wsclient.waitFinished()
			logrus.Errorln("WebsocketStop")
			time.Sleep(time.Second)
		}
	}()

	log.Printf("%+v\n", config)
	log.Infoln("Good luck!")

	go func() {
		if config.Ding != "" {
			for {
				SendDingtalkText(config.Ding, fmt.Sprintf("BuyIOC %s Runing", config.Symbol))
				time.Sleep(time.Hour)
			}
		}
	}()

	for time.Now().Before(config.StartAt) {
		time.Sleep(time.Second)
	}

	var (
		checkDur = time.Millisecond * 200
		quickDur = time.Millisecond * 10
	)
	if config.CheckDur != "" {
		checkDur, _ = time.ParseDuration(config.CheckDur)
	}
	if config.QuickDur != "" {
		quickDur, _ = time.ParseDuration(config.QuickDur)
	}
	started := false

	// 协程1： 等待开始并获取余额准备卖出
	go func() {
		for !started {
			time.Sleep(time.Millisecond * 100)
		}

		base := strings.Split(config.Symbol, "/")[0]
		for {
			balances, err := client.GetBalances()
			if err != nil {
				logrus.WithError(err).Infoln("client.GetBalances")
				time.Sleep(time.Millisecond * 100)
				continue
			}

			balance := BalanceOfCoin(balances, base)
			logrus.WithField("coin", base).Printf("%+v", balance)
			if balance.Free > 10 {
				place(uuid.New().String(), config.Symbol, "sell", config.SellPrice, "limit", balance.Free-1, false, false, false)
			}

			time.Sleep(time.Millisecond * 100)
		}
	}()

	for {
		go func() {
			err := place(uuid.New().String(), config.Symbol, "buy", config.Price, "limit", config.Qty, false, false, config.Ioc)
			if err == nil {
				started = true
			}
		}()

		if !started {
			time.Sleep(checkDur)
			continue
		} else {
			time.Sleep(quickDur)
		}
	}

	select {}
}
