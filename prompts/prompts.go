package prompts

import _ "embed"

//go:embed select_topic_system.txt
var SelectTopicSystem string

//go:embed select_article_system.txt
var SelectArticleSystem string

//go:embed generate_card_news_system.txt
var GenerateCardNewsSystem string
