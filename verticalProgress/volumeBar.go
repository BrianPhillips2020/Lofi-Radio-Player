// Vertical volume bar which can be used
package verticalprogress

import (
	tea "charm.land/bubbletea/v2"
)

type Model struct {
	height int //total height of the volume bar
	fill   int //fill amount. We're just doing incremenets of 5 - 10 because fuck you
}

const (
	DefaultHalfFull          = '▬' //may use this someday
	DefaultFullCharFullBlock = '█' //section for full fill
	DefaultEmptyCharBlock    = "░" //section for empty
)

// increase progress by this amount
type increaseVolumeMsg float64

// decrease progress by this amount
type decreaseVolumeMsg float64

// Updates the amount of the bar that's currently full
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {

}

// Render the volume bar
func (m Model) View() string {
	var bars []string
	for i := 0; i < m.height; i++ {
		if m.height-1-i <= m.fill {

		}
	}

}

// Init exists to satisfy the tea.Model interface.
func (m Model) Init() tea.Cmd {
	return nil
}
