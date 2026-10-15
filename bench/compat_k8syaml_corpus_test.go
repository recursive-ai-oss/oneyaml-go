package bench

// k8sBuiltin holds Kubernetes-shaped documents for the compatibility tests.
var k8sBuiltin = []string{
	`apiVersion: v1
kind: Pod
metadata:
  name: demo
  namespace: default
  labels:
    app: demo
    tier: "1"
    enabled: yes
  annotations:
    note: "a: b"
spec:
  containers:
  - name: web
    image: nginx:1.25
    args: ["--port", "8080", "--debug=on"]
    ports:
    - containerPort: 80
      protocol: TCP
    resources:
      limits:
        cpu: 500m
        memory: 128Mi
      requests:
        cpu: "0.5"
        memory: 64Mi
    env:
    - name: A
      value: 1
    - name: B
      value: true
    - name: C
      value: 1.50
    - name: D
      value: 0755
  restartPolicy: Always
`,
	`base: &base
  a: 1
  b: 2
derived:
  <<: *base
  b: 3
  c: 4
list:
- <<: [*base, {z: 9}]
  y: 8
`,
	"{}\n",
	"[]\n",
	"",
	"# only a comment\n",
	"---\n",
	"a: 1\n---\nb: 2\n",
	"1: one\ntrue: yes\n1.5: x\n0x10: hex\n",
	"- 1\n- 1.0\n- 1e3\n- 0b101\n- 0o17\n- 017\n- 1_000\n- .inf\n- -.inf\n- ~\n- null\n- Null\n- 12:30\n- 2001-12-14\n- 2001-12-14t21:59:43.10-05:00\n- 18446744073709551615\n- 18446744073709551616\n- 9223372036854775808\n- -9223372036854775808\n- -9223372036854775809\n",
	"a: !!str 123\nb: !!int \"7\"\nc: !!float 3\nd: !!binary aGVsbG8=\ne: !custom foo\nf: !!null ''\ng: !!bool yes\n",
	"s: |\n  line1\n  line2\nt: >\n  folded\n  text\nu: 'it''s'\nv: \"tab\\there\"\n",
	"text: \"a <b> & c \\u2028 d\"\n",
	"x: [a, b, {c: d}]\ny: {k: [1, 2]}\n",
	"a: 1\na: 2\n",
	"? [1, 2]\n: x\n",
	"~: null key\n",
	"a: &x [1, 2]\nb: *x\nc: &y {k: v}\nd: *y\n",
	"yes: 1\nno: 2\non: 3\noff: 4\ny: 5\nn: 6\n",
}
