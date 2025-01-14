package parser

import (
	"fmt"
	"strings"

	cfg "github.com/mbrt/gmailctl/internal/engine/config/v1alpha3"
	"github.com/mbrt/gmailctl/internal/errors"
	"github.com/mbrt/gmailctl/internal/reporting"
)

// Rule is an intermediate representation of a Gmail filter.
type Rule struct {
	Criteria CriteriaAST
	Actions  Actions
}

// Actions contains the actions to be applied to a set of emails.
type Actions cfg.Actions

func allChildrenLeaves(tree CriteriaAST) bool {
	t, ok := tree.(*Node)
	if !ok {
		return false
	}
	for _, child := range t.Children {
		if !child.IsLeaf() {
			return false
		}
	}
	return true
}

// Parse parses config file rules into their intermediate representation.
//
// Note that the number of rules and their contents might be different than the
// original, because symplifications will be performed on the data.
func Parse(config cfg.Config) ([]Rule, error) {
	res := []Rule{}
	for i, rule := range config.Rules {
		r, err := parseRule(rule)
		if err != nil {
			return nil, errors.WithDetails(
				fmt.Errorf("rule #%d: %w", i, err),
				fmt.Sprintf("Rule: %s", reporting.Prettify(rule, false)),
			)
		}

		root, ok := r.Criteria.(*Node)
		if ok {
			left, ok := root.Children[0].(*Node)
			// {a b c} -{x y z} ->
			//    a -{x y z}
			//    b -{x y z}
			//    c -{x y z}
			if ok &&
				allChildrenLeaves(root.Children[0]) &&
				len(root.Children) == 2 &&
				root.RootOperation() == OperationAnd &&
				len(left.Children) > 1 &&
				root.Children[0].RootOperation() == OperationOr &&
				root.Children[1].RootOperation() == OperationNot {
				for _, child := range left.Children {
					var children []CriteriaAST
					children = append(children, child)
					children = append(children, root.Children[1].(*Node).Clone())
					res = append(res, Rule{
						Criteria: &Node{
							Children:  children,
							Operation: OperationAnd,
						},
						Actions: r.Actions,
					})
				}
			} else {

				fmt.Println("left leaf?")
				leaf, ok := root.Children[0].(*Leaf)

				if ok &&
					root.RootOperation() == OperationAnd &&
					root.Children[0].RootOperation() == OperationOr &&
					len(leaf.Args) > 1 {
					fmt.Println("yep")

					for _, arg := range leaf.Args {
						var children []CriteriaAST
						children = append(children, &Leaf{
							Function: FunctionList,
							Grouping: OperationOr,
							Args:     []string{arg},
							IsRaw:    leaf.IsRaw,
						})
						for _, rem_child := range root.Children[1:] {
							children = append(children, rem_child.(*Node).Clone())
						}
						res = append(res, Rule{
							Criteria: &Node{
								Children:  children,
								Operation: OperationAnd,
							},
							Actions: r.Actions,
						})
					}

				} else {
					fmt.Println("left is not leaf")
				}
			}
		} else {
			res = append(res, r)
		}
	}
	return res, nil
}

func parseRule(rule cfg.Rule) (Rule, error) {
	res := Rule{}

	crit, err := parseCriteria(rule.Filter)
	if err != nil {
		return res, fmt.Errorf("parsing criteria: %w", err)
	}
	scrit, err := SimplifyCriteria(crit)
	if err != nil {
		return res, fmt.Errorf("simplifying criteria: %w", err)
	}
	if rule.Actions.Empty() {
		return res, errors.New("empty action")
	}

	return Rule{
		Criteria: scrit,
		Actions:  Actions(rule.Actions),
	}, nil
}

func parseCriteria(f cfg.FilterNode) (CriteriaAST, error) {
	if err := checkSyntax(f); err != nil {
		return nil, err
	}

	// Since the node is valid, only one function will be present.
	// This means that we can stop checking after the first valid field.
	if op, children := parseOperation(f); op != OperationNone {
		var astchildren []CriteriaAST
		for _, c := range children {
			astc, err := parseCriteria(c)
			if err != nil {
				return nil, err
			}
			astchildren = append(astchildren, astc)
		}
		return &Node{
			Operation: op,
			Children:  astchildren,
		}, nil
	}
	if fn, arg := parseFunction(f); fn != FunctionNone {
		return &Leaf{
			Function: fn,
			Grouping: OperationNone,
			Args:     []string{arg},
			IsRaw:    f.IsEscaped,
		}, nil
	}

	return nil, errors.New("empty filter node")
}

func checkSyntax(f cfg.FilterNode) error {
	fs := f.NonEmptyFields()
	if len(fs) != 1 {
		if len(fs) == 0 {
			return errors.New("empty filter node")
		}
		return fmt.Errorf("multiple fields specified in the same filter node: %s",
			strings.Join(fs, ","))
	}
	if !f.IsEscaped {
		return nil
	}

	// Check that 'isRaw' is used correctly
	allowed := []string{"from", "to", "subject"}
	for _, s := range allowed {
		if fs[0] == s {
			return nil
		}
	}
	return fmt.Errorf("'isRaw' can be used only with fields %s", strings.Join(allowed, ", "))
}

func parseOperation(f cfg.FilterNode) (OperationType, []cfg.FilterNode) {
	if len(f.And) > 0 {
		return OperationAnd, f.And
	}
	if len(f.Or) > 0 {
		return OperationOr, f.Or
	}
	if f.Not != nil {
		return OperationNot, []cfg.FilterNode{*f.Not}
	}
	return OperationNone, nil
}

func parseFunction(f cfg.FilterNode) (FunctionType, string) {
	if f.From != "" {
		return FunctionFrom, f.From
	}
	if f.To != "" {
		return FunctionTo, f.To
	}
	if f.Cc != "" {
		return FunctionCc, f.Cc
	}
	if f.Bcc != "" {
		return FunctionBcc, f.Bcc
	}
	if f.ReplyTo != "" {
		return FunctionReplyTo, f.ReplyTo
	}
	if f.Subject != "" {
		return FunctionSubject, f.Subject
	}
	if f.List != "" {
		return FunctionList, f.List
	}
	if f.Has != "" {
		return FunctionHas, f.Has
	}
	if f.Query != "" {
		return FunctionQuery, f.Query
	}
	return FunctionNone, ""
}
