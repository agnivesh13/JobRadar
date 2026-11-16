package score

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// skillTerms maps every spelling to one canonical skill name. The fit score
// counts how many of the skills a posting mentions are also in the resume.
var skillTerms = map[string]string{
	"python": "python", "java": "java", "javascript": "javascript", "js": "javascript", "typescript": "typescript",
	"c++": "c++", "cpp": "c++", "c#": "c#", "golang": "go", "rust": "rust", "kotlin": "kotlin",
	"scala": "scala", "ruby": "ruby", "php": "php", "sql": "sql", "nosql": "nosql",
	"postgresql": "postgres", "postgres": "postgres", "mysql": "mysql", "mongodb": "mongodb", "mongo": "mongodb",
	"redis": "redis", "kafka": "kafka", "rabbitmq": "rabbitmq", "sqs": "sqs", "elasticsearch": "elasticsearch",
	"cassandra": "cassandra", "dynamodb": "dynamodb", "snowflake": "snowflake", "bigquery": "bigquery",
	"spark": "spark", "hadoop": "hadoop", "airflow": "airflow", "pandas": "pandas", "numpy": "numpy",
	"scikit-learn": "scikit-learn", "sklearn": "scikit-learn", "tensorflow": "tensorflow", "pytorch": "pytorch",
	"keras": "keras", "llm": "llm", "llms": "llm", "rag": "rag", "nlp": "nlp", "computer vision": "computer vision",
	"deep learning": "deep learning", "machine learning": "machine learning", "ml": "machine learning",
	"data structures": "dsa", "algorithms": "dsa", "dsa": "dsa", "system design": "system design",
	"distributed systems": "distributed systems", "microservices": "microservices",
	"rest api": "rest", "rest apis": "rest", "restful": "rest", "graphql": "graphql", "grpc": "grpc", "websockets": "websockets", "websocket": "websockets",
	"fastapi": "fastapi", "flask": "flask", "django": "django", "spring boot": "spring",
	"node.js": "node.js", "nodejs": "node.js", "express.js": "express", "expressjs": "express",
	"react": "react", "react.js": "react", "reactjs": "react", "angular": "angular", "vue": "vue", "next.js": "next.js",
	"html": "html", "css": "css", "tailwind": "tailwind",
	"aws": "aws", "ec2": "aws", "s3": "aws", "lambda": "aws", "gcp": "gcp", "azure": "azure",
	"docker": "docker", "kubernetes": "kubernetes", "k8s": "kubernetes", "terraform": "terraform",
	"ci/cd": "ci/cd", "jenkins": "jenkins", "github actions": "ci/cd", "linux": "linux", "git": "git", "nginx": "nginx",
	"asyncio": "asyncio", "oauth": "oauth", "backend": "backend", "back-end": "backend", "frontend": "frontend",
	"front-end": "frontend", "full stack": "full stack", "full-stack": "full stack", "apis": "api", "api": "api",
	"cloud": "cloud", "devops": "devops", "networking": "networking", "testing": "testing",
	"trading": "trading", "quantitative": "quant", "quant": "quant", "statistics": "statistics",
	"probability": "statistics", "low latency": "low latency", "low-latency": "low latency",
	"data pipelines": "data pipelines", "data pipeline": "data pipelines", "etl": "data pipelines",
	"shap": "shap", "xgboost": "gradient boosting", "gradient boosting": "gradient boosting",
}

var (
	skillOnce sync.Once
	skillRe   *regexp.Regexp
)

// skillsIn returns the set of canonical skills mentioned in text. One
// alternation regex, longest spelling first, with boundaries that respect
// "+", "#" and "." so "c++" and "node.js" match whole.
func skillsIn(text string) map[string]bool {
	skillOnce.Do(func() {
		terms := make([]string, 0, len(skillTerms))
		for t := range skillTerms {
			terms = append(terms, regexp.QuoteMeta(t))
		}
		sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
		skillRe = regexp.MustCompile(`(?:^|[^a-z0-9+#])(` + strings.Join(terms, "|") + `)(?:[^a-z0-9+#]|$)`)
	})
	found := map[string]bool{}
	text = strings.ToLower(text)
	for pos := 0; pos < len(text); {
		m := skillRe.FindStringSubmatchIndex(text[pos:])
		if m == nil {
			break
		}
		found[skillTerms[text[pos+m[2]:pos+m[3]]]] = true
		pos += m[3] // resume right after the term so a shared separator is not skipped
	}
	return found
}

// skillScore gives up to 40 points for overlap between the posting's skills
// and the resume's.
func skillScore(in Input, s Settings, roleOK bool) (int, string) {
	jobSkills := skillsIn(in.Title + "\n" + in.Description)
	if strings.TrimSpace(s.ResumeText) == "" {
		if roleOK {
			return 22, "no resume set, role keyword matches"
		}
		return 8, "no resume set"
	}
	if len(jobSkills) == 0 {
		if roleOK {
			return 15, "posting lists no recognisable skills"
		}
		return 5, "posting lists no recognisable skills"
	}
	resume := skillsIn(s.ResumeText)
	var shared []string
	for sk := range jobSkills {
		if resume[sk] {
			shared = append(shared, sk)
		}
	}
	sort.Strings(shared)
	// Full marks at six shared skills, or all of them when the posting lists fewer.
	need := min(len(jobSkills), 6)
	pts := 40 * min(len(shared), need) / need
	if len(shared) == 0 {
		return 0, "no skill overlap with resume"
	}
	show := shared
	if len(show) > 6 {
		show = show[:6]
	}
	return pts, fmt.Sprintf("skills %d/%d: %s", len(shared), len(jobSkills), strings.Join(show, ", "))
}
