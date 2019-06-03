package alg

type TreeNode struct {
	n   Node
	env StoreEnv // Encapsulates storage, but makes a node 4 words instead of 2.
}

func EmptyTree(env StoreEnv) TreeNode {
	return TreeNode{nullNode, env}
}

func (t *TreeNode) Find(key Key) *TreeNode {
	return nil
}
