/*
	这是参照链接的解析方式解析火星名片

				链接 							 火星名片
	[test](test.jpg)			@仲达@(8659)
*/

package parse

import (
	"github.com/88250/lute/ast"
	"github.com/88250/lute/lex"
)

// parseUserCard 解析 @，可能是火星名片标记符开始 @ 也可能是普通文本 @。
func (t *Tree) parseUserCard(ctx *InlineContext) (ret *ast.Node) {
	currentAt := []byte{ctx.tokens[ctx.pos]}
	startPos := ctx.pos
	ctx.pos++

	// 获取最新一个 @
	opener := ctx.brackets

	// 如果前面没有，那么视为普通的文本
	if nil == opener {
		ret = &ast.Node{Type: ast.NodeText, Tokens: ctx.tokens[startPos:ctx.pos]}
		t.addBracket(ret, ctx.pos-1, false, ctx)
		return
	}

	if !opener.active {
		t.removeBracket(ctx)
		return &ast.Node{Type: ast.NodeText, Tokens: currentAt}
	}

	// 检查是否满足火星名片规则

	var openAt, dest, closeAt []byte
	savepos := ctx.pos
	matched := false

	// 尝试解析火星名片 @仲达@(8659)
	if ctx.pos+1 < ctx.tokensLen && lex.ItemOpenParen == ctx.tokens[ctx.pos] {
		ctx.pos++
		isLink := false
		var passed, remains []byte

		for { // 这里使用 for 是为了简化逻辑，不是为了循环
			// 判断是不是链接
			if isLink, passed, remains = lex.Spnl(ctx.tokens[ctx.pos-1:]); !isLink {
				break
			}
			ctx.pos += len(passed)
			if passed, remains, dest = t.Context.parseInlineLinkDest(remains); nil == passed {
				break
			}
			if t.Context.ParseOption.VditorWYSIWYG || t.Context.ParseOption.VditorIR || t.Context.ParseOption.VditorSV || t.Context.ParseOption.ProtyleWYSIWYG {
				if nil == opener.node.Next {
					break
				}
			}
			ctx.pos += len(passed)
			openAt = passed[0:1]
			closeAt = passed[len(passed)-1:]
			matched = lex.ItemCloseParen == passed[len(passed)-1]
			if matched {
				ctx.pos--
				break
			}
			if 1 > len(remains) || !lex.IsWhitespace(remains[0]) {
				break
			}
			break
		}
		if !matched {
			ctx.pos = savepos
		}
	}

	if matched {
		node := &ast.Node{Type: ast.NodeUserCard, LinkType: 8}
		node.AppendChild(&ast.Node{Type: ast.NodeAt, Tokens: opener.node.Tokens})

		var tmp, next *ast.Node
		tmp = opener.node.Next
		for nil != tmp {
			next = tmp.Next
			tmp.Unlink()
			if ast.NodeText == tmp.Type {
				tmp.Type = ast.NodeLinkText
			}
			node.AppendChild(tmp)
			tmp = next
		}
		node.AppendChild(&ast.Node{Type: ast.NodeAt, Tokens: currentAt})
		node.AppendChild(&ast.Node{Type: ast.NodeOpenParen, Tokens: openAt})
		node.AppendChild(&ast.Node{Type: ast.NodeLinkDest, Tokens: dest})
		node.AppendChild(&ast.Node{Type: ast.NodeCloseParen, Tokens: closeAt})
		t.processEmphasis(opener.previousDelimiter, ctx)
		t.removeBracket(ctx)
		opener.node.Unlink()

		// We remove this bracket and processEmphasis will remove later delimiters.
		// Now, for a link, we also deactivate earlier link openers.
		// (no links in links)
		opener = ctx.brackets
		for nil != opener {
			if !opener.image {
				opener.active = false // deactivate this opener
			}
			opener = opener.previous
		}

		return node
	} else { // 没有匹配到
		t.removeBracket(ctx)
		ctx.pos = startPos
		return &ast.Node{Type: ast.NodeText, Tokens: closeAt}
	}
}
