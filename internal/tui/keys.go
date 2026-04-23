package tui

type keyMap struct{}

func defaultKeyMap() keyMap {
	return keyMap{}
}

func (keyMap) hints(approval bool) string {
	if approval {
		return "enter submit | y/n decide | ctrl+b sidebar | ctrl+c quit"
	}
	return "enter send | shift+enter newline | /skill toggle | /clear | /exit | ctrl+b sidebar | ?=help"
}
