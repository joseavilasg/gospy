package browser

type Type int

const (
	Unknown Type = iota
	Chrome
	Edge
	Firefox
	Opera
	Brave
	Vivaldi
)

func (t Type) String() string {
	switch t {
	case Chrome:
		return "Chrome"
	case Edge:
		return "Edge"
	case Firefox:
		return "Firefox"
	case Opera:
		return "Opera"
	case Brave:
		return "Brave"
	case Vivaldi:
		return "Vivaldi"
	default:
		return "Unknown"
	}
}

func (t Type) IsChromium() bool {
	switch t {
	case Chrome, Edge, Opera, Brave, Vivaldi:
		return true
	default:
		return false
	}
}
