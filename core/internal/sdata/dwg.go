package sdata

import (
	"fmt"
	"strings"

	"github.com/dosco/graphjin/core/internal/util"
)

type TEdge struct {
	From, To, Weight int32

	Type RelType

	LT, RT DBTable
	L, R   DBColumn
}

func (s *DBSchema) addNode(t DBTable) int32 {
	s.tables = append(s.tables, t)
	n := s.rg.AddNode()

	s.tindex[(t.Schema + ":" + t.Name)] = nodeInfo{n}
	return n
}

func (s *DBSchema) addAliases(t DBTable, nodeID int32, aliases []string) {
	for _, al := range aliases {
		if _, ok := s.ei[(t.Schema + ":" + al)]; !ok {
			s.tindex[(t.Schema + ":" + al)] = nodeInfo{nodeID}
			s.ai[al] = nodeInfo{nodeID}
		}
	}
}

func (s *DBSchema) GetAliases() map[string]DBTable {
	ts := make(map[string]DBTable)

	for name, n := range s.ai {
		ts[name] = s.tables[int(n.nodeID)]
	}
	return ts
}

// Building the graph
// 1. AddNode is used to add tables nodes to the graph
// 2. addEdge creates relationships between schema:table -> fk_schema:fk_table
// 3. addEdge creates relationships between fk_schema:fk_table:column_name -> schema:table
// 4. addEdge creates relationships between :fk_table:column_name -> schema:table
// 5. addEdge creates relationships between :column_name -> schema:table
//
// Note 1: `_id` or `id_` is stripped from the column name to use as a graph key
// in the case where that then matches a real table name will result in conflict.
//
// Note 2: recursive relationships are kept outside the graph in `s.re`
// Eg. public.product.owner_id -> public.user.id

func (s *DBSchema) addToGraph(
	lti DBTable, lcol DBColumn,
	rti DBTable, rcol DBColumn,
	rt RelType) error {

	var rt2 RelType

	k1 := (lti.Schema + ":" + lti.Name)
	k2 := (rti.Schema + ":" + rti.Name)

	fn, ok := s.tindex[k1]
	if !ok {
		return fmt.Errorf("addEdge: unknown node: %s", k1)
	}

	tn, ok := s.tindex[k2]
	if !ok {
		return fmt.Errorf("addEdge: unknown node: %s", k2)
	}

	ln := fn.nodeID
	rn := tn.nodeID

	var weight int32 = 2
	relT := getRelName(lcol.Name)

	switch rt {
	case RelOneToOne:
		rt2 = RelOneToMany
	case RelOneToMany:
		rt2 = RelOneToOne
	case RelPolymorphic:
		rt2 = rt
		relT = rti.Name
	case RelEmbedded:
		rt2 = rt
		relT = rti.Name
		weight = 1
	case RelRecursive:
		weight = 1
	case RelRemote:
		weight = 4
		relT = rti.Name
	default:
		return nil
	}

	// Add edge from table -> foreign key table
	e1 := TEdge{
		From:   ln,
		To:     rn,
		Weight: weight,
		Type:   rt,
		LT:     lti, RT: rti,
		L: lcol, R: rcol,
	}

	// if rt == RelRecursive {
	// 	s.re[ln.ID()] = e1
	// 	return nil
	// }
	if err := s.addEdge(lti.Name, e1, false); err != nil {
		return err
	}

	// Add reverse edge from parent table -> column_name
	e2 := TEdge{
		From:   rn,
		To:     ln,
		Weight: weight,
		Type:   rt2,
		LT:     rti, RT: lti,
		L: rcol, R: lcol,
	}
	if err := s.addEdge(rti.Name, e2, false); err != nil {
		return err
	}
	if err := s.addEdge(relT, e2, false); err != nil {
		return err
	}

	// fmt.Println(">>", relT, "->", e2.RT.Name)

	// fmt.Printf("1. (%s, %d) %s.%s (%d) -> %s.%s (%d) == %s\n", lti.Name, e1.ID(), lti.Name, lcol.Name, ln.ID(), rti.Name, rcol.Name, rn.ID(), rt.String())
	// fmt.Printf("2. (%s, %d) %s.%s (%d) -> %s.%s (%d) == %s\n", rti.Name, e2.ID(), rti.Name, rcol.Name, rn.ID(), lti.Name, lcol.Name, ln.ID(), rt2.String())
	// fmt.Printf("3. (%s, %d) %s.%s (%d) -> %s.%s (%d) == %s\n", relT, e2.ID(), rti.Name, rcol.Name, rn.ID(), lti.Name, lcol.Name, ln.ID(), rt2.String())
	// fmt.Println("-----")
	return nil
}

func (s *DBSchema) addEdge(name string, edge TEdge, singular bool) error {
	edgeID, err := s.rg.AddEdge(edge.From, edge.To, edge.Weight)
	if err != nil {
		return err
	}

	ei1 := edgeInfo{nodeID: edge.From, edgeIDs: []int32{edgeID}}
	ei2 := edgeInfo{nodeID: edge.To, edgeIDs: []int32{edgeID}}

	k1 := strings.ToLower(name)
	k2 := strings.ToLower(edge.RT.Name)

	s.addEdgeInfo(k1, ei1)
	s.addEdgeInfo(k2, ei2)

	s.ae[edgeID] = edge
	return nil
}

func (s *DBSchema) addEdgeInfo(k string, ei edgeInfo) {
	if _, ok := s.ei[k]; ok {
		for i, v := range s.ei[k] {
			if v.nodeID != ei.nodeID {
				continue
			}
			for _, eid := range v.edgeIDs {
				if eid == ei.edgeIDs[0] {
					return
				}
			}
			s.ei[k][i].edgeIDs = append(s.ei[k][i].edgeIDs, ei.edgeIDs[0])
			return
		}
	}
	s.ei[k] = append(s.ei[k], ei)
}

func (s *DBSchema) Find(schema, name string) (DBTable, error) {
	var t DBTable

	if schema == "" {
		schema = s.DBSchema()
	}

	v, ok := s.tindex[(schema + ":" + name)]
	if !ok {
		return t, fmt.Errorf("table not found: %s.%s", schema, name)
	}

	return s.tables[v.nodeID], nil
}

type TPath struct {
	Rel RelType
	LT  DBTable
	LC  DBColumn
	RT  DBTable
	RC  DBColumn
}

func (s *DBSchema) FindPath(from, to, through string) ([]TPath, error) {
	fl, ok := s.ei[from]
	if !ok {
		return nil, fmt.Errorf("edge not found: %s", from)
	}

	tl, ok := s.ei[to]
	if !ok {
		return nil, fmt.Errorf("edge not found: %s", to)
	}

	// if from == to {
	// 	var edge TEdge
	// 	var ok bool

	// 	for _, v := range fl {
	// 		if edge, ok = s.re[v.nodeID]; ok {
	// 			break
	// 		}
	// 	}
	// 	if ok {
	// 		return []TPath{{
	// 			Rel: edge.Type,
	// 			LT:  edge.LT,
	// 			LC:  edge.L,
	// 			RT:  edge.RT,
	// 			RC:  edge.R,
	// 		}}, nil
	// 		// return nil, fmt.Errorf("no recursive relationship found: %s", from)
	// 	}
	// }

	if through != "" {
		if _, ok := s.tindex[(s.DBSchema() + ":" + through)]; !ok {
			return nil, fmt.Errorf("through table not found: %s.%s", s.DBSchema(), through)
		}
	}

	res := s.between(fl, tl, through)
	if len(res.edges) == 0 {
		return nil, fmt.Errorf("no relationship found: %s -> %s", from, to)
	}

	// fmt.Printf("> %s (%d) -> %s (%d)\n",
	// 	from, res.from.nodeID,
	// 	to, res.to.nodeID)

	path := []TPath{}
	for _, eid := range res.edges {
		edge := s.ae[eid]
		path = append(path, TPath{
			Rel: edge.Type,
			LT:  edge.LT,
			LC:  edge.L,
			RT:  edge.RT,
			RC:  edge.R,
		})
	}
	return path, nil
}

type graphResult struct {
	from, to edgeInfo
	edges    []int32
}

func (s *DBSchema) between(from, to []edgeInfo, through string) graphResult {
	var res graphResult

	for _, f := range from {
		for _, t := range to {
			if res, ok := s.pickPath(f, t, through); ok {
				return res
			}
		}
	}
	return res
}

func (s *DBSchema) pickPath(f, t edgeInfo, through string) (graphResult, bool) {
	var res graphResult

	fn := f.nodeID
	tn := t.nodeID
	paths := s.rg.AllPaths(fn, tn)

	for _, nodes := range paths {
		// fmt.Printf("2> %d -> %d, nodes: %d\n", f.nodeID, t.nodeID, len(nodes))
		ln := len(nodes)

		switch {
		// case 0:
		// 	return res, false

		case ln == 1:
			// lines := s.rg.GetEdges(nodes[0], nodes[0])
		// lines.Next()
		// res.edges = append(res.edges, lines.WeightedLine().ID())

		case ln == 2:
			lines := s.rg.GetEdges(nodes[0], nodes[1])
			if v := pickLine(lines, f); v != nil {
				return graphResult{f, t, []int32{v.ID}}, true
			}

		case ln > 2:
			res := graphResult{from: f, to: t}
			ln := len(nodes)

			var ff, lf bool

			for i := 1; i < ln; i++ {
				fn := nodes[i-1]
				tn := nodes[i]
				lines := s.rg.GetEdges(fn, tn)
				// printLines(lines)

				if through != "" {
					if v := pickLineByName(lines, through, s.ae); v != nil {
						res.edges = append(res.edges, v.ID)
						continue
					}
				}

				switch i {
				case 1:
					if v := pickLine(lines, f); v != nil {
						res.edges = append(res.edges, v.ID)
						ff = true
					}
				case (ln - 1):
					if v := pickLine(lines, t); v != nil {
						res.edges = append(res.edges, v.ID)
						lf = true
					}
				default:
					v := minWeightedLine(lines)
					res.edges = append(res.edges, v.ID)
				}

				if ff && lf {
					return res, true
				}
			}
		}
	}

	return res, false
}

func pickLine(lines []util.Edge, ei edgeInfo) *util.Edge {
	for _, v := range lines {
		for _, eid := range ei.edgeIDs {
			if v.ID == eid {
				return &v
			}
		}
	}
	return nil
}

func pickLineByName(lines []util.Edge, table string, ae map[int32]TEdge) *util.Edge {
	for _, v := range lines {
		e := ae[v.ID]
		if e.LT.Name == table || e.RT.Name == table {
			return &v
		}
	}
	return nil
}

func PathToRel(p TPath) DBRel {
	return DBRel{
		Type:  p.Rel,
		Left:  DBRelLeft{Ti: p.LT, Col: p.LC},
		Right: DBRelRight{Ti: p.RT, Col: p.RC},
	}
}

func minWeightedLine(lines []util.Edge) *util.Edge {
	var min int32 = 100
	var line *util.Edge

	for _, v := range lines {
		if v.Weight < min {
			min = v.Weight
			line = &v
		}
	}
	return line
}

// func printLines(lines []util.Edge) {
// 	for _, v := range lines {
// 	for lines.Next() {
// 		e := (lines.WeightedLine()).(TEdge)
// 		fmt.Printf("- (%d) %d -> %d\n", e.ID(), e.From().ID(), e.To().ID())
// 	}
// 	lines.Reset()
// }
