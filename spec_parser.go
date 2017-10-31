package cli

import (
	"fmt"

	"github.com/jawher/mow.cli/internal/lexer"
)

func uParse(c *Cmd) (*state, error) {
	tokens, err := lexer.Tokenize(c.Spec)
	if err != nil {
		return nil, err
	}

	p := &uParser{cmd: c, tokens: tokens}
	return p.parse()
}

type uParser struct {
	cmd    *Cmd
	tokens []*lexer.Token

	tkpos int

	matchedToken *lexer.Token

	rejectOptions bool
}

func (p *uParser) parse() (s *state, err error) {
	defer func() {
		if v := recover(); v != nil {
			pos := len(p.cmd.Spec)
			if !p.eof() {
				pos = p.token().Pos
			}
			s = nil
			switch t, ok := v.(string); ok {
			case true:
				err = &lexer.ParseError{Input: p.cmd.Spec, Msg: t, Pos: pos}
			default:
				panic(v)
			}
		}
	}()
	err = nil
	var e *state
	s, e = p.seq(false)
	if !p.eof() {
		s = nil
		err = &lexer.ParseError{Input: p.cmd.Spec, Msg: "Unexpected input", Pos: p.token().Pos}
		return
	}

	e.terminal = true
	s.simplify()
	return
}

func (p *uParser) seq(required bool) (*state, *state) {
	start := newState(p.cmd)
	end := start

	appendComp := func(s, e *state) {
		for _, tr := range s.transitions {
			end.t(tr.matcher, tr.next)
		}
		end = e
	}

	if required {
		s, e := p.choice()
		appendComp(s, e)
	}
	for p.canAtom() {
		s, e := p.choice()
		appendComp(s, e)
	}

	return start, end
}

func (p *uParser) choice() (*state, *state) {
	start, end := newState(p.cmd), newState(p.cmd)

	add := func(s, e *state) {
		start.t(shortcut, s)
		e.t(shortcut, end)
	}

	add(p.atom())
	for p.found(lexer.TTChoice) {
		add(p.atom())
	}
	return start, end
}

func (p *uParser) atom() (*state, *state) {
	start := newState(p.cmd)
	var end *state
	switch {
	case p.eof():
		panic("Unexpected end of input")
	case p.found(lexer.TTPos):
		name := p.matchedToken.Val
		arg, declared := p.cmd.argsIdx[name]
		if !declared {
			p.back()
			panic(fmt.Sprintf("Undeclared arg %s", name))
		}
		end = start.t(arg, newState(p.cmd))
	case p.found(lexer.TTOptions):
		if p.rejectOptions {
			p.back()
			panic("No options after --")
		}
		end = newState(p.cmd)
		start.t(optsMatcher{options: p.cmd.options, optionsIndex: p.cmd.optionsIdx}, end)
	case p.found(lexer.TTShortOpt):
		if p.rejectOptions {
			p.back()
			panic("No options after --")
		}
		name := p.matchedToken.Val
		opt, declared := p.cmd.optionsIdx[name]
		if !declared {
			p.back()
			panic(fmt.Sprintf("Undeclared option %s", name))
		}
		end = start.t(&optMatcher{
			theOne:     opt,
			optionsIdx: p.cmd.optionsIdx,
		}, newState(p.cmd))
		p.found(lexer.TTOptValue)
	case p.found(lexer.TTLongOpt):
		if p.rejectOptions {
			p.back()
			panic("No options after --")
		}
		name := p.matchedToken.Val
		opt, declared := p.cmd.optionsIdx[name]
		if !declared {
			p.back()
			panic(fmt.Sprintf("Undeclared option %s", name))
		}
		end = start.t(&optMatcher{
			theOne:     opt,
			optionsIdx: p.cmd.optionsIdx,
		}, newState(p.cmd))
		p.found(lexer.TTOptValue)
	case p.found(lexer.TTOptSeq):
		if p.rejectOptions {
			p.back()
			panic("No options after --")
		}
		end = newState(p.cmd)
		sq := p.matchedToken.Val
		opts := []*opt{}
		for i := range sq {
			sn := sq[i : i+1]
			opt, declared := p.cmd.optionsIdx["-"+sn]
			if !declared {
				p.back()
				panic(fmt.Sprintf("Undeclared option %s", sn))
			}
			opts = append(opts, opt)
		}
		start.t(optsMatcher{options: opts, optionsIndex: p.cmd.optionsIdx}, end)
	case p.found(lexer.TTOpenPar):
		start, end = p.seq(true)
		p.expect(lexer.TTClosePar)
	case p.found(lexer.TTOpenSq):
		start, end = p.seq(true)
		start.t(shortcut, end)
		p.expect(lexer.TTCloseSq)
	case p.found(lexer.TTDoubleDash):
		p.rejectOptions = true
		end = start.t(optsEnd, newState(p.cmd))
		return start, end
	default:
		panic("Unexpected input: was expecting a command or a positional argument or an option")
	}
	if p.found(lexer.TTRep) {
		end.t(shortcut, start)
	}
	return start, end
}

func (p *uParser) canAtom() bool {
	switch {
	case p.is(lexer.TTPos):
		return true
	case p.is(lexer.TTOptions):
		return true
	case p.is(lexer.TTShortOpt):
		return true
	case p.is(lexer.TTLongOpt):
		return true
	case p.is(lexer.TTOptSeq):
		return true
	case p.is(lexer.TTOpenPar):
		return true
	case p.is(lexer.TTOpenSq):
		return true
	case p.is(lexer.TTDoubleDash):
		return true
	default:
		return false
	}
}

func (p *uParser) found(t lexer.TokenType) bool {
	if p.is(t) {
		p.matchedToken = p.token()
		p.tkpos++
		return true
	}
	return false
}

func (p *uParser) is(t lexer.TokenType) bool {
	if p.eof() {
		return false
	}
	return p.token().Typ == t
}

func (p *uParser) expect(t lexer.TokenType) {
	if !p.found(t) {
		panic(fmt.Sprintf("Was expecting %v", t))
	}
}

func (p *uParser) back() {
	p.tkpos--
}
func (p *uParser) eof() bool {
	return p.tkpos >= len(p.tokens)
}

func (p *uParser) token() *lexer.Token {
	if p.eof() {
		return nil
	}

	return p.tokens[p.tkpos]
}
