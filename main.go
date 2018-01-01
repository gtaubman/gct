package main

import (
	"flag"
	"fmt"
	"math"
	"strings"
	"time"

	ws "github.com/gorilla/websocket"
	termbox "github.com/nsf/termbox-go"
	gdax "github.com/preichenberger/go-gdax"
)

var crypto = flag.String(
	"crypto", "BTC", "Crypto to show.  Supports BTC, ETH, LTC, and BCH.")

var fiat = flag.String(
	"fiat", "USD", "Fiat currency to show.  Supports USD, EUR, GBP (USD only for BCH).")

var candleSize = flag.Duration(
	"candle_size", 15*time.Minute, "Candlestick window size.")

var volumeWidth = flag.Int("volume_width", 11, "Width of the volume pane.")

var tradeWidth = flag.Int("trade_width", 14, "Width of the trade pane.")

func print_tb(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		termbox.SetCell(x, y, c, fg, bg)
		x++
	}
}

func printf_tb(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	print_tb(x, y, fg, bg, s)
}

func DrawBox(x, y, w, h int, fg, bg termbox.Attribute) {
	for i := 0; i < h; i++ {
		termbox.SetCell(x, y+i, '│', fg, bg)
		termbox.SetCell(x+w, y+i, '│', fg, bg)
	}
	for i := 0; i < w; i++ {
		termbox.SetCell(x+i, y, '─', fg, bg)
		termbox.SetCell(x+i, y+h, '─', fg, bg)
	}

	termbox.SetCell(x, y, '┌', fg, bg)
	termbox.SetCell(x, y+h, '└', fg, bg)
	termbox.SetCell(x+w, y, '┐', fg, bg)
	termbox.SetCell(x+w, y+h, '┘', fg, bg)
}

func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

var connectionErrCount int = 0

func Connect(out chan gdax.Message) {
	var wsDialer ws.Dialer
	wsConn, _, err := wsDialer.Dial("wss://ws-feed.gdax.com", nil)
	if err != nil {
		println(err.Error())
	}

	subscribe := gdax.Message{
		Type: "subscribe",
		Channels: []gdax.MessageChannel{
			{
				Name: "ticker",
				ProductIds: []string{
					fmt.Sprintf("%s-%s", *crypto, *fiat),
				},
			},
		},
	}
	if err := wsConn.WriteJSON(subscribe); err != nil {
		println(err.Error())
	}

	message := gdax.Message{}

	go func() {
		for true {
			if err := wsConn.ReadJSON(&message); err != nil {
				connectionErrCount++
				time.Sleep(time.Duration(connectionErrCount*connectionErrCount) * time.Second)
				Connect(out)
				break
			}

			// It seems that the first two messages that come back are always missing
			// their side and have broken timestamps.  Skip them.
			if len(message.Side) == 0 {
				continue
			}

			connectionErrCount = 0
			out <- message
		}
	}()
}

func GetMessages() chan gdax.Message {
	out := make(chan gdax.Message)
	Connect(out)
	return out
}

func GetEvents() chan termbox.Event {
	out := make(chan termbox.Event)
	go func() {
		for {
			out <- termbox.PollEvent()
		}
	}()
	return out
}

type Bucket struct {
	Open     float64
	Close    float64
	Max      float64
	Min      float64
	Trades   int64
	Start    time.Time
	Duration time.Duration
}
type Frame struct {
	x, y, w, h int
}

func (f *Frame) Clear() {
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.x; x++ {
			f.SetCell(x, y, ' ', termbox.ColorDefault, termbox.ColorDefault)
		}
	}
}

func (f *Frame) SetCell(x, y int, r rune, fg, bg termbox.Attribute) {
	termbox.SetCell(f.x+x, f.y+y, r, fg, bg)
}

func (f *Frame) Box(fg, bg termbox.Attribute) {
	DrawBox(f.x, f.y, f.w, f.h, fg, bg)
}

func (f *Frame) Printf(x, y int, fg, bg termbox.Attribute, format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	f.Print(x, y, fg, bg, s)
}

func (f *Frame) Print(x, y int, fg, bg termbox.Attribute, msg string) {
	for _, c := range msg {
		f.SetCell(x, y, c, fg, bg)
		x++
	}
}

func (f *Frame) PrintHeader(msg string, fg, bg termbox.Attribute) {
	f.Print(int(math.Ceil(float64(f.w)/2.0-float64(len(msg))/2.0)), 0, fg, bg, msg)
}

func DrawTrades(tradeFrame Frame, trades []gdax.Message) {
	tradeFrame.Box(termbox.ColorWhite, termbox.ColorDefault)
	for i, j := 1, len(trades)-1; j >= 0 && i < tradeFrame.h; i, j = i+1, j-1 {
		message := trades[j]

		fg := termbox.ColorGreen
		bg := termbox.ColorDefault
		if message.Side == "sell" {
			fg = termbox.ColorRed
		}
		fg |= termbox.AttrBold
		tradeFrame.Printf(1, i, fg, bg, "%-4s %.2f", message.Side, message.Price)
	}
	tradeFrame.PrintHeader("Trades", termbox.ColorWhite, termbox.ColorDefault)
}

func DrawCandles(candleFrame Frame, buckets []*Bucket) {
	candleFrame.Box(termbox.ColorWhite, termbox.ColorDefault)
	candleFrame.PrintHeader("Price", termbox.ColorWhite, termbox.ColorDefault)

	lowerBound, upperBound := math.MaxFloat32, 0.0
	for _, box := range buckets {
		lowerBound = math.Min(lowerBound, box.Min)
		upperBound = math.Max(upperBound, box.Max)
	}
	if upperBound-lowerBound < 100 {
		lowerBound -= 50
		upperBound += 50
	}
	priceSpread := upperBound - lowerBound

	for line, j := 1, len(buckets)-1; j >= 0 && line < candleFrame.h; line, j = line+1, j-1 {
		bucket := buckets[j]
		fg := termbox.ColorGreen
		bg := termbox.ColorDefault

		// Draw the legend.
		candleFrame.Printf(1, 0, fg, bg, "%.2f", lowerBound)
		s := fmt.Sprintf("%.2f", upperBound)
		candleFrame.Print(candleFrame.w-len(s), 0, fg, bg, s)

		if bucket.Close < bucket.Open {
			fg = termbox.ColorRed
		}

		// Draw the bounds.
		start := 1 + (math.Min(bucket.Min, bucket.Max)-lowerBound)/priceSpread*float64(candleFrame.w-2)
		stop := 1 + (math.Max(bucket.Min, bucket.Max)-lowerBound)/priceSpread*float64(candleFrame.w-2)
		for i := int(start); i <= int(stop); i++ {
			candleFrame.SetCell(i, line, '─', fg, bg)
		}

		// Then we draw the open/close.
		start = 1 + (math.Min(bucket.Open, bucket.Close)-lowerBound)/priceSpread*float64(candleFrame.w-2)
		stop = 1 + (math.Max(bucket.Open, bucket.Close)-lowerBound)/priceSpread*float64(candleFrame.w-2)
		for i := int(start); i <= int(stop); i++ {
			candleFrame.SetCell(i, line, '█', fg, bg)
		}
	}
}

func DrawVolume(volumeFrame Frame, buckets []*Bucket) {
	volumeFrame.Box(termbox.ColorWhite, termbox.ColorDefault)
	volumeFrame.PrintHeader("Volume", termbox.ColorWhite, termbox.ColorDefault)

	volumeUpperBound := 0.0
	for _, box := range buckets {
		volumeUpperBound = math.Max(volumeUpperBound, float64(box.Trades))
	}
	volumeSpread := volumeUpperBound

	volumeMaxStr := fmt.Sprintf("%d", int(volumeUpperBound))
	volumeFrame.Print(volumeFrame.w-len(volumeMaxStr), volumeFrame.h, termbox.ColorGreen, termbox.ColorDefault, volumeMaxStr)
	for line, j := 1, len(buckets)-1; j >= 0 && line < volumeFrame.h; line, j = line+1, j-1 {
		bucket := buckets[j]
		fg := termbox.ColorBlue
		bg := termbox.ColorDefault

		start := 1
		stop := float64(bucket.Trades) / volumeSpread * float64(volumeFrame.w-1)
		for i := int(start); i <= int(stop); i++ {
			volumeFrame.SetCell(i, line, '─', fg, bg)
		}
	}
}

func ProcessMessage(message gdax.Message, trades *[]gdax.Message, buckets *[]*Bucket) {
	*trades = append(*trades, message)

	t := message.Time.Time().Truncate(*candleSize)

	// If there are no buckets, start one.
	if len(*buckets) == 0 {
		*buckets = append(*buckets, &Bucket{
			Open:     message.Price,
			Close:    message.Price,
			Start:    t,
			Min:      math.MaxFloat32,
			Max:      0.0,
			Duration: *candleSize,
		})
	}

	bucket := (*buckets)[len(*buckets)-1]
	if (*buckets)[len(*buckets)-1].Start.Equal(t) {
		bucket.Close = message.Price
	} else {
		// Time to start a new bucket.
		*buckets = append(*buckets, &Bucket{
			Open:     message.Price,
			Close:    message.Price,
			Start:    t,
			Min:      math.MaxFloat32,
			Max:      0.0,
			Duration: *candleSize,
		})
		bucket = (*buckets)[len(*buckets)-1]
	}
	bucket.Trades++
	bucket.Max = math.Max(bucket.Max, message.Price)
	bucket.Min = math.Min(bucket.Min, message.Price)
}

func Draw(trades []gdax.Message, buckets []*Bucket) {
	width, height := termbox.Size()

	candleWidth := width - (*volumeWidth + *tradeWidth + 3)

	volumeFrame := Frame{0, 1, *volumeWidth, height - 2}
	candleFrame := Frame{*volumeWidth + 1, 1, candleWidth, height - 2}
	tradeFrame := Frame{*volumeWidth + candleWidth + 2, 1, *tradeWidth, height - 2}

	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	printf_tb(0, 0, termbox.ColorWhite, termbox.ColorDefault,
		"Crypto: %s   Fiat: %s   Exchange: GDAX   Candle Size: %s", *crypto, *fiat, shortDuration(*candleSize))

	DrawTrades(tradeFrame, trades)
	DrawCandles(candleFrame, buckets)
	DrawVolume(volumeFrame, buckets)

	termbox.Flush()
}

func main() {
	flag.Parse()

	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()
	termbox.SetInputMode(termbox.InputEsc)

	messages := GetMessages()
	events := GetEvents()
	trades := []gdax.Message{}
	buckets := []*Bucket{}

	Draw(trades, buckets)

loop:
	for {
		select {
		case message := <-messages:
			ProcessMessage(message, &trades, &buckets)
			Draw(trades, buckets)
		case ev := <-events:
			switch ev.Type {
			case termbox.EventKey:
				if ev.Key == termbox.KeyEsc {
					break loop
				}
			case termbox.EventResize:
				Draw(trades, buckets)
			}
		}
	}
}
