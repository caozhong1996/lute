// Lute - 一款对中文语境优化的 Markdown 引擎，支持 Go 和 JavaScript
// Copyright (c) 2019-present, b3log.org
//
// Lute is licensed under Mulan PSL v2.
// You can use this software according to the terms and conditions of the Mulan PSL v2.
// You may obtain a copy of Mulan PSL v2 at:
//         http://license.coscl.org.cn/MulanPSL2
// THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND, EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT, MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
// See the Mulan PSL v2 for more details.

package lute

import (
	"bytes"
	"strings"
)

// parseBlocks 解析并生成块级节点。
func (t *Tree) parseBlocks() {
	t.context.tip = t.Root
	t.context.linkRefDefs = map[string]*Node{}
	t.context.footnotesDefs = []*Node{}
	lines := 0
	for line := t.lexer.nextLine(); nil != line; line = t.lexer.nextLine() {
		if t.context.option.VditorWYSIWYG {
			ln := []rune(string(line))
			if 4 < len(ln) && isDigit(byte(ln[0])) && ('、' == ln[1] || '）' == ln[1]) {
				// 列表标记符自动优化 https://github.com/Vanessa219/vditor/issues/68
				line = []byte(string(ln[0]) + ". " + string(ln[2:]))
			}
		}

		t.incorporateLine(line)
		lines++
	}
	for nil != t.context.tip {
		t.context.finalize(t.context.tip, lines)
	}
}

// incorporateLine 处理文本行 line 并把生成的块级节点挂到树上。
func (t *Tree) incorporateLine(line []byte) {
	t.context.oldtip = t.context.tip
	t.context.offset = 0
	t.context.column = 0
	t.context.blank = false
	t.context.partiallyConsumedTab = false
	t.context.lineNum++
	t.context.currentLine = line
	t.context.currentLineLen = len(t.context.currentLine)

	allMatched := true
	var container *Node
	container = t.Root
	lastChild := container.LastChild
	for ; nil != lastChild && !lastChild.Close; lastChild = container.LastChild {
		container = lastChild
		t.context.findNextNonspace()

		switch container.Continue(t.context) {
		case 0: // 说明匹配可继续处理
			break
		case 1: // 匹配失败，不能继续处理
			allMatched = false
			break
		case 2: // 匹配围栏代码块闭合，处理下一行
			return
		}

		if !allMatched {
			container = container.Parent // 回到上一个匹配的块
			break
		}
	}

	t.context.allClosed = container == t.context.oldtip
	t.context.lastMatchedContainer = container

	matchedLeaf := container.Type != NodeParagraph && container.AcceptLines()
	startsLen := len(blockStarts)

	// 除非最后一个匹配到的是代码块，否则的话就起始一个新的块级节点
	for !matchedLeaf {
		t.context.findNextNonspace()

		// 如果不由潜在的节点标记符开头 ^[#`~*+_=<>0-9-$]，则说明不用继续迭代生成子节点
		// 这里仅做简单判断的话可以提升一些性能
		maybeMarker := t.context.currentLine[t.context.nextNonspace]
		if !t.context.indented && // 缩进代码块
			itemHyphen != maybeMarker && itemAsterisk != maybeMarker && itemPlus != maybeMarker && // 无序列表
			!isDigit(maybeMarker) && // 有序列表
			itemBacktick != maybeMarker && itemTilde != maybeMarker && // 代码块
			itemCrosshatch != maybeMarker && // ATX 标题
			itemGreater != maybeMarker && // 块引用
			itemLess != maybeMarker && // HTML 块
			itemUnderscore != maybeMarker && itemEqual != maybeMarker && // Setext 标题
			itemDollar != maybeMarker && // 数学公式
			itemOpenBracket != maybeMarker { // 脚注
			t.context.advanceNextNonspace()
			break
		}

		// 逐个尝试是否可以起始一个块级节点
		var i = 0
		for i < startsLen {
			var res = blockStarts[i](t, container)
			if res == 1 { // 匹配到容器块，继续迭代下降过程
				container = t.context.tip
				break
			} else if res == 2 { // 匹配到叶子块，跳出迭代下降过程
				container = t.context.tip
				matchedLeaf = true
				break
			} else { // 没有匹配到，继续用下一个起始块模式进行匹配
				i++
			}
		}

		if i == startsLen { // nothing matched
			t.context.advanceNextNonspace()
			break
		}
	}

	// offset 后余下的内容算作是文本行，需要将其添加到相应的块节点上

	if !t.context.allClosed && !t.context.blank && t.context.tip.Type == NodeParagraph {
		// 该行是段落延续文本，直接添加到当前末梢段落上
		t.addLine()
	} else {
		// 最终化未匹配的块
		t.context.closeUnmatchedBlocks()

		if t.context.blank && nil != container.LastChild {
			container.LastChild.LastLineBlank = true
		}

		typ := container.Type
		isFenced := NodeCodeBlock == typ && container.IsFencedCodeBlock

		// 空行判断，主要是为了判断列表是紧凑模式还是松散模式
		var lastLineBlank = t.context.blank &&
			!(typ == NodeFootnotesDef ||
				typ == NodeBlockquote || // 块引用行肯定不会是空行因为至少有一个 >
				(typ == NodeCodeBlock && isFenced) || // 围栏代码块不计入空行判断
				(typ == NodeMathBlock) || // 数学公式块不计入空行判断
				(typ == NodeListItem && nil == container.FirstChild)) // 内容为空的列表项也不计入空行判断
		// 因为列表是块级容器（可进行嵌套），所以需要在父节点方向上传播 LastLineBlank
		// LastLineBlank 目前仅在判断列表紧凑模式上使用
		for cont := container; nil != cont; cont = cont.Parent {
			cont.LastLineBlank = lastLineBlank
		}

		if container.AcceptLines() {
			t.addLine()
			if typ == NodeHTMLBlock {
				// HTML 块（类型 1-5）需要检查是否满足闭合条件
				html := container
				if html.HtmlBlockType >= 1 && html.HtmlBlockType <= 5 {
					tokens := t.context.currentLine[t.context.offset:]
					if t.isHTMLBlockClose(tokens, html.HtmlBlockType) {
						t.context.finalize(container, t.context.lineNum)
					}
				}
			}
		} else if t.context.offset < t.context.currentLineLen && !t.context.blank {
			// 普通段落开始
			t.context.addChild(NodeParagraph, t.context.offset)
			t.context.advanceNextNonspace()
			t.addLine()
		}
	}
}

// blockStartFunc 定义了用于判断块是否开始的函数签名。
type blockStartFunc func(t *Tree, container *Node) int

// blockStarts 定义了一系列函数，每个函数用于判断某种块节点是否可以开始，返回值：
// 0：不匹配
// 1：匹配到块容器，需要继续迭代下降
// 2：匹配到叶子块
var blockStarts = []blockStartFunc{

	// 判断脚注定义（[^label]）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.option.Footnotes {
			return 0
		}

		if !t.context.indented {
			marker := peek(t.context.currentLine, t.context.nextNonspace)
			if itemOpenBracket != marker {
				return 0
			}
			caret := peek(t.context.currentLine, t.context.nextNonspace+1)
			if itemCaret != caret {
				return 0
			}

			var label = []byte{itemCaret}
			var token byte
			var i int
			for i = t.context.nextNonspace + 2; i < t.context.currentLineLen; i++ {
				token = t.context.currentLine[i]
				if itemSpace == token || itemNewline == token || itemTab == token {
					return 0
				}
				if itemCloseBracket == token {
					break
				}
				label = append(label, token)
			}
			if i >= t.context.currentLineLen {
				return 0
			}
			if itemColon != t.context.currentLine[i+1] {
				return 0
			}
			t.context.advanceOffset(1, false)

			t.context.closeUnmatchedBlocks()
			t.context.advanceOffset(len(label)+2, true)
			footnotesDef := t.context.addChild(NodeFootnotesDef, t.context.nextNonspace)
			footnotesDef.Tokens = label
			lowerCaseLabel := bytes.ToLower(label)
			if _, def := t.context.findFootnotesDef(lowerCaseLabel); nil == def {
				t.context.footnotesDefs = append(t.context.footnotesDefs, footnotesDef)
			}
			return 1
		}
		return 0
	},

	// 判断块引用（>）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented {
			marker := peek(t.context.currentLine, t.context.nextNonspace)
			if itemGreater == marker {
				markers := []byte{marker}
				t.context.advanceNextNonspace()
				t.context.advanceOffset(1, false)
				// > 后面的空格是可选的
				whitespace := peek(t.context.currentLine, t.context.offset)
				withSpace := itemSpace == whitespace || itemTab == whitespace
				if withSpace {
					t.context.advanceOffset(1, true)
					markers = append(markers, whitespace)
				}
				if t.context.option.VditorWYSIWYG {
					// Vditor 所见即所得模式下块引用标记符 > 后面不能为空
					ln := bytesToStr(t.context.currentLine[t.context.offset:])
					ln = strings.ReplaceAll(ln, caret, "")
					if ln = strings.TrimSpace(ln); "" == ln {
						return 0
					}
				}
				t.context.closeUnmatchedBlocks()
				t.context.addChild(NodeBlockquote, t.context.nextNonspace)
				t.context.addChildMarker(NodeBlockquoteMarker, markers)
				return 1
			}
		}
		return 0
	},

	// 判断 ATX 标题（#）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented {
			if ok, markers, content, level := t.parseATXHeading(); ok {
				t.context.advanceNextNonspace()
				t.context.advanceOffset(len(content), false)
				t.context.closeUnmatchedBlocks()
				heading := t.context.addChild(NodeHeading, t.context.nextNonspace)
				heading.HeadingLevel = level
				heading.Tokens = content
				crosshatchMarker := &Node{Type: NodeHeadingC8hMarker, Tokens: markers}
				heading.AppendChild(crosshatchMarker)
				t.context.advanceOffset(t.context.currentLineLen-t.context.offset, false)
				return 2
			}
		}
		return 0
	},

	// 判断围栏代码块（```）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented {
			if ok, codeBlockFenceChar, codeBlockFenceLen, codeBlockFenceOffset, codeBlockOpenFence, codeBlockInfo := t.parseFencedCode(); ok {
				t.context.closeUnmatchedBlocks()
				container := t.context.addChild(NodeCodeBlock, t.context.nextNonspace)
				container.IsFencedCodeBlock = true
				container.CodeBlockFenceLen = codeBlockFenceLen
				container.CodeBlockFenceChar = codeBlockFenceChar
				container.CodeBlockFenceOffset = codeBlockFenceOffset
				container.CodeBlockOpenFence = codeBlockOpenFence
				container.CodeBlockInfo = codeBlockInfo
				t.context.advanceNextNonspace()
				t.context.advanceOffset(codeBlockFenceLen, false)
				return 2
			}
		}
		return 0
	},

	// 判断 Setext 标题（- =）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented && container.Type == NodeParagraph {
			if level := t.parseSetextHeading(); 0 != level {
				if t.context.option.GFMTable {
					// 尝试解析表，因为可能出现如下情况：
					//
					//   0
					//   -:
					//   -
					//
					// 前两行可以解析出一个只有一个单元格的表。
					// Empty list following GFM Table makes table broken https://github.com/b3log/lute/issues/9
					table := t.context.parseTable(container)
					if nil != table {
						// 将该段落节点转成表节点
						container.Type = NodeTable
						container.TableAligns = table.TableAligns
						for tr := table.FirstChild; nil != tr; {
							nextTr := tr.Next
							container.AppendChild(tr)
							tr = nextTr
						}
						container.Tokens = nil
						return 0
					}
				}

				t.context.closeUnmatchedBlocks()
				// 解析链接引用定义
				for tokens := container.Tokens; 0 < len(tokens) && itemOpenBracket == tokens[0]; tokens = container.Tokens {
					if remains := t.context.parseLinkRefDef(tokens); nil != remains {
						container.Tokens = remains
					} else {
						break
					}
				}

				if value := container.Tokens; 0 < len(value) {
					child := &Node{Type: NodeHeading, HeadingLevel: level}
					child.Tokens = trimWhitespace(value)
					container.InsertAfter(child)
					container.Unlink()
					t.context.tip = child
					t.context.advanceOffset(t.context.currentLineLen-t.context.offset, false)
					return 2
				}
			}
		}
		return 0
	},

	// 判断 HTML 块（<）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented && peek(t.context.currentLine, t.context.nextNonspace) == itemLess {
			tokens := t.context.currentLine[t.context.nextNonspace:]
			if htmlType := t.parseHTML(tokens); 0 != htmlType {
				t.context.closeUnmatchedBlocks()
				block := t.context.addChild(NodeHTMLBlock, t.context.offset)
				block.HtmlBlockType = htmlType
				return 2
			}
		}
		return 0
	},

	// 判断分隔线（--- ***）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented {
			if ok, caretTokens := t.parseThematicBreak(); ok {
				t.context.closeUnmatchedBlocks()
				thematicBreak := t.context.addChild(NodeThematicBreak, t.context.nextNonspace)
				thematicBreak.Tokens = caretTokens
				t.context.advanceOffset(t.context.currentLineLen-t.context.offset, false)
				return 2
			}
		}
		return 0
	},

	// 判断列表、列表项（* - + 1.）或者任务列表项是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented || container.Type == NodeList {
			data := t.parseListMarker(container)
			if nil == data {
				return 0
			}

			t.context.closeUnmatchedBlocks()

			listsMatch := container.Type == NodeList && t.context.listsMatch(container.ListData, data)
			if t.context.tip.Type != NodeList || !listsMatch {
				list := t.context.addChild(NodeList, t.context.nextNonspace)
				list.ListData = data
			}
			listItem := t.context.addChild(NodeListItem, t.context.nextNonspace)
			listItem.ListData = data
			listItem.Tokens = data.Marker
			if 1 == listItem.ListData.Typ || (3 == listItem.ListData.Typ && 0 == listItem.ListData.BulletChar) {
				// 修正有序列表项序号
				prev := listItem.Previous
				if nil != prev {
					listItem.Num = prev.Num + 1
				} else {
					listItem.Num = data.Start
				}
			}

			return 1
		}
		return 0
	},

	// 判断数学公式块（$$）是否开始
	func(t *Tree, container *Node) int {
		if !t.context.indented {
			if ok, mathBlockDollarOffset := t.parseMathBlock(); ok {
				t.context.closeUnmatchedBlocks()
				block := t.context.addChild(NodeMathBlock, t.context.nextNonspace)
				block.MathBlockDollarOffset = mathBlockDollarOffset
				t.context.advanceNextNonspace()
				t.context.advanceOffset(mathBlockDollarOffset, false)
				return 2
			}
		}
		return 0
	},

	// 判断缩进代码块（    code）是否开始
	func(t *Tree, container *Node) int {
		if t.context.indented && t.context.tip.Type != NodeParagraph && !t.context.blank {
			t.context.advanceOffset(4, true)
			t.context.closeUnmatchedBlocks()
			t.context.addChild(NodeCodeBlock, t.context.offset)
			return 2
		}
		return 0
	},
}

// addLine 用于在当前的末梢节点 context.tip 上添加迭代行剩余的所有 Tokens。
// 调用该方法前必须确认末梢 tip 能够接受新行。
func (t *Tree) addLine() {
	if t.context.partiallyConsumedTab {
		t.context.offset++ // skip over tab
		// add space characters:
		var charsToTab = 4 - (t.context.column % 4)
		for i := 0; i < charsToTab; i++ {
			t.context.tip.AppendTokens(strToBytes(" "))
		}
	}
	t.context.tip.AppendTokens(t.context.currentLine[t.context.offset:])
}
