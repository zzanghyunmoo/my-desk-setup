package ui

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Choice struct {
	ID    string
	Label string
}

type chooser struct {
	choices  []Choice
	cursor   int
	selected map[string]bool
	done     bool
	canceled bool
}

func newChooser(choices []Choice) chooser {
	copy := append([]Choice(nil), choices...)
	sort.Slice(copy, func(left, right int) bool {
		return copy[left].ID < copy[right].ID
	})
	return chooser{choices: copy, selected: make(map[string]bool)}
}

func (model chooser) Init() tea.Cmd {
	return nil
}

func (model chooser) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return model, nil
	}
	switch key.String() {
	case "ctrl+c", "esc", "q":
		model.canceled = true
		return model, tea.Quit
	case "up", "k":
		if model.cursor > 0 {
			model.cursor--
		}
	case "down", "j":
		if model.cursor < len(model.choices)-1 {
			model.cursor++
		}
	case " ":
		if len(model.choices) > 0 {
			id := model.choices[model.cursor].ID
			model.selected[id] = !model.selected[id]
		}
	case "enter":
		model.done = true
		return model, tea.Quit
	}
	return model, nil
}

func (model chooser) View() string {
	if model.done || model.canceled {
		return ""
	}
	var view strings.Builder
	view.WriteString("Select components (space toggle, enter confirm, q cancel)\n\n")
	for index, choice := range model.choices {
		cursor := " "
		if index == model.cursor {
			cursor = ">"
		}
		mark := " "
		if model.selected[choice.ID] {
			mark = "x"
		}
		_, _ = fmt.Fprintf(&view, "%s [%s] %-24s %s\n", cursor, mark, choice.ID, choice.Label)
	}
	return view.String()
}

func (model chooser) selection() []string {
	var selection []string
	for id, selected := range model.selected {
		if selected {
			selection = append(selection, id)
		}
	}
	sort.Strings(selection)
	return selection
}

func Choose(choices []Choice, input io.Reader, output io.Writer) ([]string, error) {
	if len(choices) == 0 {
		return nil, errors.New("interactive chooser has no components")
	}
	program := tea.NewProgram(
		newChooser(choices),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	result, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("interactive chooser: %w", err)
	}
	final, ok := result.(chooser)
	if !ok {
		return nil, errors.New("interactive chooser returned an unexpected model")
	}
	if final.canceled {
		return nil, errors.New("interactive selection canceled")
	}
	selection := final.selection()
	if len(selection) == 0 {
		return nil, errors.New("interactive selection is empty")
	}
	return selection, nil
}

func Choices(idsAndLabels map[string]string) []Choice {
	choices := make([]Choice, 0, len(idsAndLabels))
	for id, label := range idsAndLabels {
		choices = append(choices, Choice{ID: id, Label: label})
	}
	sort.Slice(choices, func(left, right int) bool {
		return choices[left].ID < choices[right].ID
	})
	return choices
}
