package command

import "container/list"

var history = list.New()
var historyNode *list.Element

func AddToHistory(cmd string) {
	if cmd == "" {
		return
	}
	history.PushFront(cmd)
	historyNode = nil
}

func GetPreviousHistory() string {
	if history.Len() == 0 {
		return ""
	}
	if historyNode == nil {
		historyNode = history.Front()
	} else if historyNode.Next() != nil {
		historyNode = historyNode.Next()
	}
	return historyNode.Value.(string)
}

func GetNextHistory() string {
	if historyNode == nil || historyNode.Prev() == nil {
		historyNode = nil
		return ""
	}
	historyNode = historyNode.Prev()
	return historyNode.Value.(string)
}
