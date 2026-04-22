package tui

type keyMap struct{}

func defaultKeyMap() keyMap {
	return keyMap{}
}

func (keyMap) hints(approval bool) string {
	if approval {
		return "enter submit | y/n decide | ctrl+c quit"
	}
	return "enter send | /skill toggle | /clear | /exit | wheel scroll"
}
