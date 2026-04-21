package repl

import "strings"

var builtinCommands = []string{"help", "history", "tools", "skills", "clear", "exit"}

type Command struct {
	Name string
	Args []string
}

func ParseCommand(line string) (Command, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return Command{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(fields) == 0 {
		return Command{}, false
	}
	return Command{
		Name: strings.ToLower(fields[0]),
		Args: fields[1:],
	}, true
}

func BuiltinCommands() []string {
	out := make([]string, len(builtinCommands))
	copy(out, builtinCommands)
	return out
}

func IsBuiltinCommand(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, builtin := range builtinCommands {
		if name == builtin {
			return true
		}
	}
	return false
}
