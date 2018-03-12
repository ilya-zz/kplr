package kql

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/alecthomas/participle"
	"github.com/alecthomas/participle/lexer"
)

var (
	kqlLexer = lexer.Unquote(lexer.Upper(lexer.Must(lexer.Regexp(`(\s+)`+
		`|(?P<Keyword>(?i)SELECT|FORMAT|WHERE|POSITION|LIMIT|OFFSET|AND|OR|LIKE|CONTAINS|PREFIX|SUFFIX|NOT)`+
		`|(?P<Ident>[a-zA-Z0-9-_@#$%?&*{}]+)`+
		`|(?P<String>'[^']*'|"[^"]*")`+
		`|(?P<Operator><>|!=|<=|>=|[-+*/%,.=<>()])`,
	)), "Keyword"), "String")
	parser     = participle.MustBuild(&Select{}, kqlLexer)
	parserExpr = participle.MustBuild(&Expression{}, kqlLexer)
)

const (
	CMP_CONTAINS   = "CONTAINS"
	CMP_HAS_PREFIX = "PREFIX"
	CMP_HAS_SUFFIX = "SUFFIX"
	CMP_LIKE       = "LIKE"
)

type (
	Int int64

	Select struct {
		Tail     bool        `"SELECT" `
		Format   string      `["FORMAT" @String]`
		Where    *Expression `["WHERE" @@]`
		Position *Position   `["POSITION" @@]`
		Offset   *int64      `["OFFSET" @Ident]`
		Limit    int64       `"LIMIT" @Ident`
	}

	Expression struct {
		Or []*OrCondition `@@ { "OR" @@ }`
	}

	OrCondition struct {
		And []*XCondition `@@ { "AND" @@ }`
	}

	XCondition struct {
		Not  bool        ` [@"NOT"] `
		Cond *Condition  `( @@`
		Expr *Expression `| "(" @@ ")")`
	}

	Condition struct {
		Operand string `  @Ident`
		Op      string ` (@("<"|">"|">="|"<="|"!="|"="|"CONTAINS"|"PREFIX"|"SUFFIX"|"LIKE"))`
		Value   string ` (@Ident|@String)`
	}

	Position struct {
		PosId string `(@"TAIL"|@"HEAD"|@String|@Ident)`
	}
)

func (i *Int) Capture(values []string) error {
	v, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return err
	}
	if v < 0 {
		return errors.New(fmt.Sprint("Expecting positive integer, but ", v))
	}
	*i = Int(v)
	return nil
}

func Parse(kql string) (*Select, error) {
	sel := &Select{}
	err := parser.ParseString(kql, sel)
	if err != nil {
		return nil, err
	}
	return sel, err
}

func ParseExpr(where string) (*Expression, error) {
	exp := &Expression{}
	err := parserExpr.ParseString(where, exp)
	if err != nil {
		return nil, err
	}
	return exp, err
}