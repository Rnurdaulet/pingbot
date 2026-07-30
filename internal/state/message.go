package state

// Button is an inline keyboard button (Telegram callback_data).
type Button struct {
	Label string
	Data  string
}

// Message is an outbound alert/notification.
type Message struct {
	Text    string
	HTML    bool
	Buttons [][]Button
}

// Notifier delivers alert messages (Telegram, etc.).
type Notifier interface {
	SendMessage(msg Message) error
}
