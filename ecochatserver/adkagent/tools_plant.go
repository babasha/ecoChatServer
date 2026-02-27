package adkagent

import (
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ============================================================================
// PLANT KNOWLEDGE TOOLS — Static database of 89 plant species
// ============================================================================

// PlantSpecies represents a single plant in the database
type PlantSpecies struct {
	Name         string  // Common name (English)
	NameRu       string  // Russian name
	NameDe       string  // German name
	Scientific   string  // Latin name
	Category     string  // tropical, succulent, herb, vegetable, flowering
	HumidityMin  int     // Min soil moisture %
	HumidityMax  int     // Max soil moisture %
	TempMin      float64 // Min temperature C
	TempMax      float64 // Max temperature C
	Light        string  // bright_indirect, low, direct, partial
	Watering     string  // Watering frequency description
	Difficulty   string  // beginner, intermediate, advanced
	Edible       bool
	Tags         []string
	CareNotes    string
}

// plantDatabase — embedded database of 89 plant species with Zefir sensor thresholds
var plantDatabase = []PlantSpecies{
	// ===== TROPICAL (25 species) =====
	{Name: "Monstera Deliciosa", NameRu: "Монстера", NameDe: "Fensterblatt", Scientific: "Monstera deliciosa", Category: "tropical", HumidityMin: 40, HumidityMax: 60, TempMin: 18, TempMax: 30, Light: "bright_indirect", Watering: "When top 2-3cm dry", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "large"}, CareNotes: "Loves humidity. Wipe leaves monthly. Support with moss pole."},
	{Name: "Pothos", NameRu: "Потос", NameDe: "Efeutute", Scientific: "Epipremnum aureum", Category: "tropical", HumidityMin: 30, HumidityMax: 55, TempMin: 15, TempMax: 30, Light: "low", Watering: "When soil is dry", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "trailing", "low-light"}, CareNotes: "Very forgiving. Tolerates low light. Trim to encourage bushiness."},
	{Name: "Peace Lily", NameRu: "Спатифиллум", NameDe: "Einblatt", Scientific: "Spathiphyllum wallisii", Category: "tropical", HumidityMin: 40, HumidityMax: 65, TempMin: 16, TempMax: 28, Light: "low", Watering: "Keep consistently moist", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "flowering", "low-light"}, CareNotes: "Droops when thirsty — great visual indicator. Toxic to pets."},
	{Name: "Fiddle Leaf Fig", NameRu: "Фикус лировидный", NameDe: "Geigenfeige", Scientific: "Ficus lyrata", Category: "tropical", HumidityMin: 35, HumidityMax: 55, TempMin: 18, TempMax: 30, Light: "bright_indirect", Watering: "When top 3cm dry", Difficulty: "intermediate", Tags: []string{"popular", "large", "statement"}, CareNotes: "Sensitive to drafts and relocation. Rotate quarterly."},
	{Name: "Rubber Plant", NameRu: "Фикус эластика", NameDe: "Gummibaum", Scientific: "Ficus elastica", Category: "tropical", HumidityMin: 35, HumidityMax: 55, TempMin: 16, TempMax: 30, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "large"}, CareNotes: "Wipe leaves to maintain shine. Prune to control shape."},
	{Name: "Snake Plant", NameRu: "Сансевиерия", NameDe: "Bogenhanf", Scientific: "Sansevieria trifasciata", Category: "tropical", HumidityMin: 20, HumidityMax: 40, TempMin: 13, TempMax: 35, Light: "low", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "low-light", "drought-tolerant"}, CareNotes: "Nearly indestructible. Overwatering is main risk."},
	{Name: "ZZ Plant", NameRu: "Замиокулькас", NameDe: "Zamioculcas", Scientific: "Zamioculcas zamiifolia", Category: "tropical", HumidityMin: 20, HumidityMax: 40, TempMin: 15, TempMax: 30, Light: "low", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"popular", "low-light", "drought-tolerant"}, CareNotes: "Stores water in rhizomes. Very low maintenance."},
	{Name: "Calathea", NameRu: "Калатея", NameDe: "Korbmarante", Scientific: "Calathea spp.", Category: "tropical", HumidityMin: 50, HumidityMax: 70, TempMin: 18, TempMax: 27, Light: "low", Watering: "Keep moist, never soggy", Difficulty: "advanced", Tags: []string{"prayer-plant", "patterned", "humidity-lover"}, CareNotes: "Needs high humidity. Use distilled water. Leaves fold at night."},
	{Name: "Philodendron", NameRu: "Филодендрон", NameDe: "Philodendron", Scientific: "Philodendron spp.", Category: "tropical", HumidityMin: 35, HumidityMax: 60, TempMin: 16, TempMax: 30, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"popular", "air-purifier", "trailing"}, CareNotes: "Many varieties available. Great hanging plant. Toxic to pets."},
	{Name: "Alocasia", NameRu: "Алоказия", NameDe: "Alocasia", Scientific: "Alocasia spp.", Category: "tropical", HumidityMin: 45, HumidityMax: 65, TempMin: 18, TempMax: 30, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"dramatic", "large-leaves"}, CareNotes: "Needs consistent humidity. May go dormant in winter."},
	{Name: "Bird of Paradise", NameRu: "Стрелиция", NameDe: "Strelitzie", Scientific: "Strelitzia reginae", Category: "tropical", HumidityMin: 35, HumidityMax: 55, TempMin: 15, TempMax: 30, Light: "direct", Watering: "When top 3cm dry", Difficulty: "intermediate", Tags: []string{"large", "statement", "flowering"}, CareNotes: "Needs bright light for flowers. Can grow very large indoors."},
	{Name: "Anthurium", NameRu: "Антуриум", NameDe: "Flamingoblume", Scientific: "Anthurium andraeanum", Category: "tropical", HumidityMin: 45, HumidityMax: 65, TempMin: 18, TempMax: 28, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "intermediate", Tags: []string{"flowering", "air-purifier"}, CareNotes: "Blooms year-round with good care. Needs humidity."},
	{Name: "Dracaena", NameRu: "Драцена", NameDe: "Drachenbaum", Scientific: "Dracaena spp.", Category: "tropical", HumidityMin: 25, HumidityMax: 50, TempMin: 15, TempMax: 30, Light: "low", Watering: "When top half dry", Difficulty: "beginner", Tags: []string{"air-purifier", "low-light", "tall"}, CareNotes: "Sensitive to fluoride in tap water. Good air purifier."},
	{Name: "Croton", NameRu: "Кротон", NameDe: "Kroton", Scientific: "Codiaeum variegatum", Category: "tropical", HumidityMin: 40, HumidityMax: 60, TempMin: 18, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"colorful", "statement"}, CareNotes: "More light = more color. Drops leaves if moved. High humidity."},
	{Name: "Dieffenbachia", NameRu: "Диффенбахия", NameDe: "Dieffenbachie", Scientific: "Dieffenbachia spp.", Category: "tropical", HumidityMin: 35, HumidityMax: 60, TempMin: 16, TempMax: 30, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"large-leaves", "air-purifier"}, CareNotes: "Toxic sap — wear gloves when pruning. Good for offices."},
	{Name: "Fern (Boston)", NameRu: "Папоротник бостонский", NameDe: "Schwertfarn", Scientific: "Nephrolepis exaltata", Category: "tropical", HumidityMin: 50, HumidityMax: 70, TempMin: 15, TempMax: 27, Light: "bright_indirect", Watering: "Keep consistently moist", Difficulty: "intermediate", Tags: []string{"humidity-lover", "hanging", "air-purifier"}, CareNotes: "Loves humidity. Mist regularly. Great in bathrooms."},
	{Name: "Begonia Rex", NameRu: "Бегония Рекс", NameDe: "Königsbegonie", Scientific: "Begonia rex", Category: "tropical", HumidityMin: 45, HumidityMax: 65, TempMin: 16, TempMax: 27, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "intermediate", Tags: []string{"patterned", "colorful"}, CareNotes: "Beautiful foliage. Avoid water on leaves. Needs humidity."},
	{Name: "Chinese Evergreen", NameRu: "Аглаонема", NameDe: "Kolbenfaden", Scientific: "Aglaonema spp.", Category: "tropical", HumidityMin: 30, HumidityMax: 55, TempMin: 16, TempMax: 28, Light: "low", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"low-light", "air-purifier", "colorful"}, CareNotes: "Very tolerant of neglect. Many color varieties."},
	{Name: "Schefflera", NameRu: "Шеффлера", NameDe: "Strahlenaralie", Scientific: "Schefflera arboricola", Category: "tropical", HumidityMin: 30, HumidityMax: 55, TempMin: 15, TempMax: 28, Light: "bright_indirect", Watering: "When top 3cm dry", Difficulty: "beginner", Tags: []string{"tall", "umbrella-plant"}, CareNotes: "Also called Umbrella Plant. Prune to maintain compact shape."},
	{Name: "Maranta", NameRu: "Маранта", NameDe: "Pfeilwurz", Scientific: "Maranta leuconeura", Category: "tropical", HumidityMin: 45, HumidityMax: 65, TempMin: 16, TempMax: 27, Light: "low", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"prayer-plant", "patterned", "low-light"}, CareNotes: "Leaves fold at night (prayer plant). Needs humidity."},
	{Name: "Syngonium", NameRu: "Сингониум", NameDe: "Purpurtute", Scientific: "Syngonium podophyllum", Category: "tropical", HumidityMin: 35, HumidityMax: 60, TempMin: 16, TempMax: 30, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"trailing", "air-purifier"}, CareNotes: "Leaf shape changes as plant matures. Easy to propagate."},
	{Name: "Hoya", NameRu: "Хойя", NameDe: "Wachsblume", Scientific: "Hoya carnosa", Category: "tropical", HumidityMin: 30, HumidityMax: 50, TempMin: 15, TempMax: 28, Light: "bright_indirect", Watering: "When fully dry", Difficulty: "beginner", Tags: []string{"flowering", "trailing", "fragrant"}, CareNotes: "Waxy leaves and fragrant flowers. Let dry between waterings."},
	{Name: "Tradescantia", NameRu: "Традесканция", NameDe: "Dreimasterblume", Scientific: "Tradescantia zebrina", Category: "tropical", HumidityMin: 30, HumidityMax: 55, TempMin: 15, TempMax: 28, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"trailing", "colorful", "fast-growing"}, CareNotes: "Very fast grower. Pinch tips for fuller growth. Easy to propagate."},
	{Name: "Ctenanthe", NameRu: "Ктенанта", NameDe: "Kammmarante", Scientific: "Ctenanthe spp.", Category: "tropical", HumidityMin: 45, HumidityMax: 65, TempMin: 16, TempMax: 27, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"prayer-plant", "patterned"}, CareNotes: "Related to calathea. Needs consistent humidity and warmth."},
	{Name: "Banana Plant", NameRu: "Банан комнатный", NameDe: "Bananenpflanze", Scientific: "Musa spp.", Category: "tropical", HumidityMin: 40, HumidityMax: 65, TempMin: 18, TempMax: 32, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"large", "fast-growing", "statement"}, CareNotes: "Needs lots of light and water. Fast grower in good conditions."},

	// ===== SUCCULENT (20 species) =====
	{Name: "Aloe Vera", NameRu: "Алоэ вера", NameDe: "Echte Aloe", Scientific: "Aloe vera", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 13, TempMax: 35, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Edible: true, Tags: []string{"medicinal", "drought-tolerant", "popular"}, CareNotes: "Medicinal gel inside leaves. Let soil dry completely between waterings."},
	{Name: "Jade Plant", NameRu: "Крассула", NameDe: "Geldbaum", Scientific: "Crassula ovata", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 10, TempMax: 30, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"popular", "drought-tolerant", "long-lived"}, CareNotes: "Can live for decades. Leaves wrinkle when thirsty."},
	{Name: "Echeveria", NameRu: "Эхеверия", NameDe: "Echeverie", Scientific: "Echeveria spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 10, TempMax: 30, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"rosette", "colorful", "compact"}, CareNotes: "Many color varieties. Avoid water on rosette. Needs good drainage."},
	{Name: "Haworthia", NameRu: "Хавортия", NameDe: "Haworthie", Scientific: "Haworthia spp.", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 10, TempMax: 30, Light: "bright_indirect", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"compact", "low-light-succulent", "window-plant"}, CareNotes: "One of few succulents that tolerates lower light. Great for windowsills."},
	{Name: "String of Pearls", NameRu: "Крестовник Роули", NameDe: "Erbsenpflanze", Scientific: "Senecio rowleyanus", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 13, TempMax: 28, Light: "bright_indirect", Watering: "When fully dry", Difficulty: "intermediate", Tags: []string{"trailing", "unique", "hanging"}, CareNotes: "Pearl-shaped leaves store water. Bottom watering recommended."},
	{Name: "Sedum", NameRu: "Седум", NameDe: "Fetthenne", Scientific: "Sedum spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 5, TempMax: 35, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"groundcover", "outdoor-indoor", "hardy"}, CareNotes: "Very hardy. Many varieties. Some are cold-hardy for outdoors."},
	{Name: "Kalanchoe", NameRu: "Каланхоэ", NameDe: "Flammendes Käthchen", Scientific: "Kalanchoe blossfeldiana", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 13, TempMax: 30, Light: "direct", Watering: "When fully dry", Difficulty: "beginner", Tags: []string{"flowering", "colorful", "compact"}, CareNotes: "Blooms in many colors. Short-day plant — needs long dark periods to re-bloom."},
	{Name: "Sempervivum", NameRu: "Молодило", NameDe: "Hauswurz", Scientific: "Sempervivum tectorum", Category: "succulent", HumidityMin: 10, HumidityMax: 25, TempMin: -20, TempMax: 35, Light: "direct", Watering: "Every 3-4 weeks", Difficulty: "beginner", Tags: []string{"hardy", "outdoor", "rosette"}, CareNotes: "Extremely cold-hardy. Also called Hens and Chicks. Great outdoors."},
	{Name: "Lithops", NameRu: "Литопс", NameDe: "Lebende Steine", Scientific: "Lithops spp.", Category: "succulent", HumidityMin: 5, HumidityMax: 20, TempMin: 10, TempMax: 35, Light: "direct", Watering: "Rarely, follow cycle", Difficulty: "advanced", Tags: []string{"unique", "living-stones", "compact"}, CareNotes: "Living stones. Very specific watering cycle. Do NOT water during split."},
	{Name: "Agave", NameRu: "Агава", NameDe: "Agave", Scientific: "Agave spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 5, TempMax: 35, Light: "direct", Watering: "Every 3-4 weeks", Difficulty: "beginner", Tags: []string{"large", "architectural", "drought-tolerant"}, CareNotes: "Sharp leaf tips — handle carefully. Very drought tolerant."},
	{Name: "Gasteria", NameRu: "Гастерия", NameDe: "Gasterie", Scientific: "Gasteria spp.", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 10, TempMax: 30, Light: "partial", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"compact", "low-light-succulent"}, CareNotes: "Related to aloe. Tolerates lower light than most succulents."},
	{Name: "Senecio", NameRu: "Крестовник", NameDe: "Kreuzkraut", Scientific: "Senecio spp.", Category: "succulent", HumidityMin: 15, HumidityMax: 30, TempMin: 10, TempMax: 30, Light: "bright_indirect", Watering: "When fully dry", Difficulty: "intermediate", Tags: []string{"trailing", "unique"}, CareNotes: "Many trailing varieties. Prone to overwatering."},
	{Name: "Aeonium", NameRu: "Эониум", NameDe: "Aeonium", Scientific: "Aeonium spp.", Category: "succulent", HumidityMin: 15, HumidityMax: 35, TempMin: 5, TempMax: 30, Light: "direct", Watering: "When dry", Difficulty: "beginner", Tags: []string{"rosette", "colorful", "tall"}, CareNotes: "Winter grower — dormant in summer. Reduce water in summer."},
	{Name: "Euphorbia", NameRu: "Молочай", NameDe: "Wolfsmilch", Scientific: "Euphorbia spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 10, TempMax: 35, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"cactus-like", "architectural"}, CareNotes: "Toxic milky sap — handle with care. Looks like cactus but isn't."},
	{Name: "Cactus (Mammillaria)", NameRu: "Кактус маммиллярия", NameDe: "Warzenkaktus", Scientific: "Mammillaria spp.", Category: "succulent", HumidityMin: 5, HumidityMax: 25, TempMin: 5, TempMax: 38, Light: "direct", Watering: "Every 3-4 weeks", Difficulty: "beginner", Tags: []string{"flowering", "compact", "cactus"}, CareNotes: "Easy flowering cactus. Needs winter cool period for blooms."},
	{Name: "Christmas Cactus", NameRu: "Шлюмбергера", NameDe: "Weihnachtskaktus", Scientific: "Schlumbergera spp.", Category: "succulent", HumidityMin: 30, HumidityMax: 50, TempMin: 10, TempMax: 25, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"flowering", "holiday", "hanging"}, CareNotes: "Blooms in winter. Needs short days and cool nights to trigger flowering."},
	{Name: "Prickly Pear", NameRu: "Опунция", NameDe: "Feigenkaktus", Scientific: "Opuntia spp.", Category: "succulent", HumidityMin: 5, HumidityMax: 25, TempMin: 0, TempMax: 38, Light: "direct", Watering: "Every 3-4 weeks", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "outdoor", "cactus"}, CareNotes: "Pads and fruit are edible. Very hardy. Watch for hidden glochids."},
	{Name: "Pachyphytum", NameRu: "Пахифитум", NameDe: "Mondstein", Scientific: "Pachyphytum spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 10, TempMax: 30, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"compact", "pastel", "rosette"}, CareNotes: "Chubby leaves with powdery coating. Don't touch the coating."},
	{Name: "Graptoveria", NameRu: "Граптоверия", NameDe: "Graptoveria", Scientific: "x Graptoveria spp.", Category: "succulent", HumidityMin: 10, HumidityMax: 30, TempMin: 10, TempMax: 30, Light: "direct", Watering: "Every 2-3 weeks", Difficulty: "beginner", Tags: []string{"rosette", "colorful", "hybrid"}, CareNotes: "Hybrid of Graptopetalum and Echeveria. Sun-stress brings out colors."},

	// ===== HERBS (15 species) =====
	{Name: "Basil", NameRu: "Базилик", NameDe: "Basilikum", Scientific: "Ocimum basilicum", Category: "herb", HumidityMin: 40, HumidityMax: 65, TempMin: 18, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "annual"}, CareNotes: "Pinch flower buds for more leaves. Needs warm temperatures and sun."},
	{Name: "Mint", NameRu: "Мята", NameDe: "Minze", Scientific: "Mentha spp.", Category: "herb", HumidityMin: 40, HumidityMax: 65, TempMin: 13, TempMax: 28, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "invasive"}, CareNotes: "Very vigorous grower. Keep in its own pot — will take over!"},
	{Name: "Rosemary", NameRu: "Розмарин", NameDe: "Rosmarin", Scientific: "Salvia rosmarinus", Category: "herb", HumidityMin: 25, HumidityMax: 45, TempMin: 5, TempMax: 30, Light: "direct", Watering: "When dry", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "mediterranean"}, CareNotes: "Mediterranean herb. Needs good air circulation. Prone to mildew indoors."},
	{Name: "Thyme", NameRu: "Тимьян", NameDe: "Thymian", Scientific: "Thymus vulgaris", Category: "herb", HumidityMin: 20, HumidityMax: 40, TempMin: 5, TempMax: 30, Light: "direct", Watering: "When dry", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "mediterranean"}, CareNotes: "Prefers dry conditions. Good drainage essential. Harvest regularly."},
	{Name: "Parsley", NameRu: "Петрушка", NameDe: "Petersilie", Scientific: "Petroselinum crispum", Category: "herb", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "biennial"}, CareNotes: "Biennial — bolts in second year. Keep well-watered."},
	{Name: "Cilantro", NameRu: "Кинза", NameDe: "Koriander", Scientific: "Coriandrum sativum", Category: "herb", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "culinary", "annual"}, CareNotes: "Bolts quickly in heat. Succession sow for continuous harvest."},
	{Name: "Chives", NameRu: "Шнитт-лук", NameDe: "Schnittlauch", Scientific: "Allium schoenoprasum", Category: "herb", HumidityMin: 35, HumidityMax: 55, TempMin: 5, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "perennial"}, CareNotes: "Hardy perennial. Cut regularly to encourage growth. Edible flowers."},
	{Name: "Oregano", NameRu: "Орегано", NameDe: "Oregano", Scientific: "Origanum vulgare", Category: "herb", HumidityMin: 20, HumidityMax: 40, TempMin: 5, TempMax: 30, Light: "direct", Watering: "When dry", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "mediterranean"}, CareNotes: "Prefers lean, dry soil. More flavor when slightly stressed."},
	{Name: "Sage", NameRu: "Шалфей", NameDe: "Salbei", Scientific: "Salvia officinalis", Category: "herb", HumidityMin: 20, HumidityMax: 40, TempMin: 5, TempMax: 28, Light: "direct", Watering: "When dry", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "aromatic", "medicinal"}, CareNotes: "Woody perennial. Prune after flowering. Drought tolerant once established."},
	{Name: "Lavender", NameRu: "Лаванда", NameDe: "Lavendel", Scientific: "Lavandula spp.", Category: "herb", HumidityMin: 15, HumidityMax: 35, TempMin: 5, TempMax: 30, Light: "direct", Watering: "When dry", Difficulty: "intermediate", Edible: true, Tags: []string{"aromatic", "flowering", "mediterranean"}, CareNotes: "Needs excellent drainage and airflow. Prune after flowering."},
	{Name: "Dill", NameRu: "Укроп", NameDe: "Dill", Scientific: "Anethum graveolens", Category: "herb", HumidityMin: 35, HumidityMax: 55, TempMin: 10, TempMax: 28, Light: "direct", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "culinary", "annual"}, CareNotes: "Tall herb — may need support. Seeds also useful in cooking."},
	{Name: "Lemon Balm", NameRu: "Мелисса", NameDe: "Zitronenmelisse", Scientific: "Melissa officinalis", Category: "herb", HumidityMin: 35, HumidityMax: 55, TempMin: 5, TempMax: 28, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "aromatic", "medicinal", "tea"}, CareNotes: "Lemon-scented mint relative. Makes great tea. Vigorous spreader."},
	{Name: "Chamomile", NameRu: "Ромашка", NameDe: "Kamille", Scientific: "Matricaria chamomilla", Category: "herb", HumidityMin: 30, HumidityMax: 50, TempMin: 5, TempMax: 28, Light: "direct", Watering: "Moderate", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "medicinal", "flowering", "tea"}, CareNotes: "Flowers used for tea. Self-seeds readily. Prefers poor soil."},
	{Name: "Stevia", NameRu: "Стевия", NameDe: "Stevia", Scientific: "Stevia rebaudiana", Category: "herb", HumidityMin: 35, HumidityMax: 55, TempMin: 15, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "sweetener", "tropical-herb"}, CareNotes: "Natural sweetener. Pinch tips for bushiness. Overwinter indoors."},
	{Name: "Bay Laurel", NameRu: "Лавр", NameDe: "Lorbeer", Scientific: "Laurus nobilis", Category: "herb", HumidityMin: 30, HumidityMax: 50, TempMin: 5, TempMax: 28, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "culinary", "tree", "slow-growing"}, CareNotes: "Slow-growing tree. Leaves used in cooking. Can be shaped as topiary."},

	// ===== VEGETABLES (14 species) =====
	{Name: "Tomato (Cherry)", NameRu: "Томат черри", NameDe: "Kirschtomate", Scientific: "Solanum lycopersicum", Category: "vegetable", HumidityMin: 40, HumidityMax: 65, TempMin: 18, TempMax: 32, Light: "direct", Watering: "Consistent moisture", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fruiting", "popular"}, CareNotes: "Cherry varieties best for indoors. Needs 6-8h direct light. Pollinate by hand."},
	{Name: "Pepper (Chili)", NameRu: "Перец чили", NameDe: "Chili", Scientific: "Capsicum annuum", Category: "vegetable", HumidityMin: 35, HumidityMax: 60, TempMin: 18, TempMax: 32, Light: "direct", Watering: "When top 2cm dry", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fruiting", "spicy"}, CareNotes: "Compact chili varieties great for windowsills. Need warmth and light."},
	{Name: "Lettuce", NameRu: "Салат", NameDe: "Salat", Scientific: "Lactuca sativa", Category: "vegetable", HumidityMin: 45, HumidityMax: 65, TempMin: 10, TempMax: 22, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "leafy"}, CareNotes: "Fast-growing. Harvest outer leaves for continuous harvest. Bolts in heat."},
	{Name: "Spinach", NameRu: "Шпинат", NameDe: "Spinat", Scientific: "Spinacia oleracea", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 5, TempMax: 22, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "leafy", "nutritious"}, CareNotes: "Prefers cooler temperatures. Bolts in heat. Rich in iron."},
	{Name: "Strawberry", NameRu: "Клубника", NameDe: "Erdbeere", Scientific: "Fragaria x ananassa", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 28, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "fruiting", "popular"}, CareNotes: "Everbearing varieties best for indoors. Pollinate by hand. Runners produce new plants."},
	{Name: "Radish", NameRu: "Редис", NameDe: "Radieschen", Scientific: "Raphanus sativus", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "root"}, CareNotes: "Ready in 25-30 days! Needs consistent moisture for best roots."},
	{Name: "Green Onion", NameRu: "Зеленый лук", NameDe: "Frühlingszwiebel", Scientific: "Allium fistulosum", Category: "vegetable", HumidityMin: 35, HumidityMax: 55, TempMin: 10, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "regrowable"}, CareNotes: "Can regrow from kitchen scraps! Just put roots in water."},
	{Name: "Microgreens", NameRu: "Микрозелень", NameDe: "Microgreens", Scientific: "Various", Category: "vegetable", HumidityMin: 50, HumidityMax: 70, TempMin: 18, TempMax: 25, Light: "bright_indirect", Watering: "Mist daily", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "nutritious"}, CareNotes: "Ready in 7-14 days. Very nutritious. Sunflower and pea shoots are popular."},
	{Name: "Arugula", NameRu: "Руккола", NameDe: "Rucola", Scientific: "Eruca vesicaria", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 22, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "peppery"}, CareNotes: "Peppery flavor. Grows fast. Bolts in heat — harvest young."},
	{Name: "Swiss Chard", NameRu: "Мангольд", NameDe: "Mangold", Scientific: "Beta vulgaris", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 28, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "colorful", "leafy"}, CareNotes: "Beautiful colorful stems. Cut-and-come-again harvesting. Heat tolerant."},
	{Name: "Kale", NameRu: "Кейл", NameDe: "Grünkohl", Scientific: "Brassica oleracea", Category: "vegetable", HumidityMin: 40, HumidityMax: 60, TempMin: 5, TempMax: 25, Light: "partial", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "nutritious", "leafy", "cold-hardy"}, CareNotes: "Superfood. Tastes sweeter after frost. Very cold hardy."},
	{Name: "Pea Shoots", NameRu: "Горох (побеги)", NameDe: "Erbsensprossen", Scientific: "Pisum sativum", Category: "vegetable", HumidityMin: 45, HumidityMax: 65, TempMin: 10, TempMax: 22, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "sprouts"}, CareNotes: "Harvest in 10-14 days. Sweet flavor. Good for windowsill growing."},
	{Name: "Bean Sprouts", NameRu: "Ростки фасоли", NameDe: "Sojasprossen", Scientific: "Vigna radiata", Category: "vegetable", HumidityMin: 50, HumidityMax: 70, TempMin: 18, TempMax: 28, Light: "low", Watering: "Rinse 2x daily", Difficulty: "beginner", Edible: true, Tags: []string{"edible", "fast-growing", "sprouts"}, CareNotes: "Ready in 3-5 days! Grow in jars. Rinse twice daily."},
	{Name: "Cucumber (Mini)", NameRu: "Огурец мини", NameDe: "Minigurke", Scientific: "Cucumis sativus", Category: "vegetable", HumidityMin: 45, HumidityMax: 65, TempMin: 18, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Edible: true, Tags: []string{"edible", "fruiting", "climbing"}, CareNotes: "Compact varieties for indoors. Needs trellis. Pollinate by hand."},

	// ===== FLOWERING (15 species) =====
	{Name: "Orchid (Phalaenopsis)", NameRu: "Орхидея фаленопсис", NameDe: "Schmetterlingsorchidee", Scientific: "Phalaenopsis spp.", Category: "flowering", HumidityMin: 35, HumidityMax: 55, TempMin: 16, TempMax: 28, Light: "bright_indirect", Watering: "When roots are silver", Difficulty: "beginner", Tags: []string{"flowering", "popular", "elegant"}, CareNotes: "Most popular orchid. Water when roots turn silver. Ice cube method works well."},
	{Name: "African Violet", NameRu: "Сенполия", NameDe: "Usambaraveilchen", Scientific: "Streptocarpus sect. Saintpaulia", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 16, TempMax: 27, Light: "bright_indirect", Watering: "Bottom water", Difficulty: "beginner", Tags: []string{"flowering", "compact", "windowsill"}, CareNotes: "Bottom water only — leaves spot if wet. Blooms year-round with good care."},
	{Name: "Jasmine", NameRu: "Жасмин", NameDe: "Jasmin", Scientific: "Jasminum spp.", Category: "flowering", HumidityMin: 35, HumidityMax: 55, TempMin: 13, TempMax: 28, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"flowering", "fragrant", "climbing"}, CareNotes: "Intensely fragrant. Needs cool winter period for blooming. Climbing vine."},
	{Name: "Hibiscus", NameRu: "Гибискус", NameDe: "Hibiskus", Scientific: "Hibiscus rosa-sinensis", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 16, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"flowering", "tropical", "large-flowers"}, CareNotes: "Large colorful flowers. Needs warmth and humidity. Prune for shape."},
	{Name: "Geranium", NameRu: "Герань", NameDe: "Geranie", Scientific: "Pelargonium spp.", Category: "flowering", HumidityMin: 25, HumidityMax: 45, TempMin: 10, TempMax: 28, Light: "direct", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"flowering", "windowsill", "fragrant"}, CareNotes: "Tolerates some drought. Deadhead for continuous blooming. Many scented varieties."},
	{Name: "Cyclamen", NameRu: "Цикламен", NameDe: "Alpenveilchen", Scientific: "Cyclamen persicum", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 10, TempMax: 20, Light: "bright_indirect", Watering: "Bottom water", Difficulty: "intermediate", Tags: []string{"flowering", "winter-blooming", "cool"}, CareNotes: "Blooms in cool temperatures. Goes dormant in summer. Bottom water only."},
	{Name: "Gardenia", NameRu: "Гардения", NameDe: "Gardenie", Scientific: "Gardenia jasminoides", Category: "flowering", HumidityMin: 50, HumidityMax: 70, TempMin: 16, TempMax: 27, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "advanced", Tags: []string{"flowering", "fragrant", "demanding"}, CareNotes: "Very fragrant but demanding. Needs acidic soil, humidity, and consistent care."},
	{Name: "Bromeliad", NameRu: "Бромелия", NameDe: "Bromelie", Scientific: "Bromeliaceae spp.", Category: "flowering", HumidityMin: 35, HumidityMax: 55, TempMin: 15, TempMax: 30, Light: "bright_indirect", Watering: "Water in central cup", Difficulty: "beginner", Tags: []string{"flowering", "tropical", "colorful"}, CareNotes: "Fill central cup with water. Mother plant dies after flowering but produces pups."},
	{Name: "Lipstick Plant", NameRu: "Эсхинантус", NameDe: "Lippenstiftpflanze", Scientific: "Aeschynanthus spp.", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 16, TempMax: 28, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "intermediate", Tags: []string{"flowering", "trailing", "unique"}, CareNotes: "Red tubular flowers resemble lipstick. Great hanging plant. Needs humidity."},
	{Name: "Oxalis", NameRu: "Оксалис", NameDe: "Sauerklee", Scientific: "Oxalis triangularis", Category: "flowering", HumidityMin: 30, HumidityMax: 50, TempMin: 13, TempMax: 25, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "beginner", Tags: []string{"flowering", "purple", "compact"}, CareNotes: "Purple shamrock leaves fold at night. Goes dormant periodically — reduce water."},
	{Name: "Clivia", NameRu: "Кливия", NameDe: "Klivie", Scientific: "Clivia miniata", Category: "flowering", HumidityMin: 30, HumidityMax: 50, TempMin: 10, TempMax: 25, Light: "bright_indirect", Watering: "When top half dry", Difficulty: "beginner", Tags: []string{"flowering", "orange", "low-maintenance"}, CareNotes: "Needs cool dry winter to trigger spring blooms. Very long-lived plant."},
	{Name: "Primrose", NameRu: "Примула", NameDe: "Primel", Scientific: "Primula vulgaris", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 5, TempMax: 18, Light: "bright_indirect", Watering: "Keep moist", Difficulty: "beginner", Tags: []string{"flowering", "spring", "colorful"}, CareNotes: "Cool-season bloomer. Many colors. Can transplant outdoors after blooming."},
	{Name: "Gloxinia", NameRu: "Глоксиния", NameDe: "Gloxinie", Scientific: "Sinningia speciosa", Category: "flowering", HumidityMin: 40, HumidityMax: 65, TempMin: 16, TempMax: 25, Light: "bright_indirect", Watering: "When top 2cm dry", Difficulty: "intermediate", Tags: []string{"flowering", "velvety", "colorful"}, CareNotes: "Velvety leaves and flowers. Avoid wetting leaves. Goes dormant after blooming."},
	{Name: "Crown of Thorns", NameRu: "Молочай Миля", NameDe: "Christusdorn", Scientific: "Euphorbia milii", Category: "flowering", HumidityMin: 15, HumidityMax: 35, TempMin: 13, TempMax: 30, Light: "direct", Watering: "When fully dry", Difficulty: "beginner", Tags: []string{"flowering", "drought-tolerant", "thorny"}, CareNotes: "Blooms almost year-round. Very drought tolerant. Thorny stems — handle with care."},
	{Name: "Ixora", NameRu: "Иксора", NameDe: "Ixore", Scientific: "Ixora coccinea", Category: "flowering", HumidityMin: 40, HumidityMax: 60, TempMin: 16, TempMax: 30, Light: "direct", Watering: "Keep moist", Difficulty: "intermediate", Tags: []string{"flowering", "tropical", "cluster-flowers"}, CareNotes: "Clusters of small flowers. Needs acidic soil. Sensitive to hard water."},
}

// plantCategories lists the 5 main categories
var plantCategories = map[string]string{
	"tropical":  "Tropical & Foliage",
	"succulent": "Succulents & Cacti",
	"herb":      "Herbs & Aromatics",
	"vegetable": "Vegetables & Edibles",
	"flowering": "Flowering Plants",
}

// CreatePlantTools creates 6 plant knowledge tools (all static, no API)
func CreatePlantTools() ([]tool.Tool, error) {
	var tools []tool.Tool

	// Tool 1: search_plant
	t1, err := createSearchPlantTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t1)

	// Tool 2: get_plant_categories
	t2, err := createGetPlantCategoriesTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t2)

	// Tool 3: get_plants_by_category
	t3, err := createGetPlantsByCategoryTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t3)

	// Tool 4: get_plant_care
	t4, err := createGetPlantCareTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t4)

	// Tool 5: compare_plants
	t5, err := createComparePlantsTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t5)

	// Tool 6: recommend_plants
	t6, err := createRecommendPlantsTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, t6)

	log.Printf("[TOOLS_PLANT] Created %d plant tools (%d species in database)", len(tools), len(plantDatabase))
	return tools, nil
}

// createSearchPlantTool — search plant species by name or tag
func createSearchPlantTool() (tool.Tool, error) {
	type Input struct {
		Query string `json:"query"`
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "search_plant",
			Description: "Search plant species by name, scientific name, or tag. Use when user asks about a specific plant like 'monstera', 'aloe vera', 'basil'. Returns matching plants with humidity thresholds and care info.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] search_plant called: query=%s", input.Query)

			if input.Query == "" {
				return Output{Result: "Error: search query is required"}, nil
			}

			query := strings.ToLower(input.Query)
			var matches []PlantSpecies

			for _, p := range plantDatabase {
				if matchesPlant(p, query) {
					matches = append(matches, p)
				}
			}

			if len(matches) == 0 {
				return Output{Result: fmt.Sprintf("No plants found for '%s'. Try searching by common name (e.g., 'monstera', 'basil') or category (e.g., 'tropical', 'herb').", input.Query)}, nil
			}

			if len(matches) > 8 {
				matches = matches[:8]
			}

			result := fmt.Sprintf("Found %d plant(s) matching '%s':\n\n", len(matches), input.Query)
			for _, p := range matches {
				result += formatPlantSummary(p)
			}

			return Output{Result: result}, nil
		},
	)
}

// createGetPlantCategoriesTool — list 5 categories with counts
func createGetPlantCategoriesTool() (tool.Tool, error) {
	type Input struct{}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_plant_categories",
			Description: "Get list of plant categories with species count. Use when user asks 'what plants do you have', 'show categories', 'what types of plants'. Returns 5 categories: tropical, succulent, herb, vegetable, flowering.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_plant_categories called")

			counts := make(map[string]int)
			for _, p := range plantDatabase {
				counts[p.Category]++
			}

			result := fmt.Sprintf("Plant Database: %d species in 5 categories\n\n", len(plantDatabase))
			for key, name := range plantCategories {
				result += fmt.Sprintf("  %s — %d species\n", name, counts[key])
			}
			result += "\nUse get_plants_by_category(category) to see plants in a specific category."

			return Output{Result: result}, nil
		},
	)
}

// createGetPlantsByCategoryTool — plants in a given category
func createGetPlantsByCategoryTool() (tool.Tool, error) {
	type Input struct {
		Category string `json:"category"` // tropical, succulent, herb, vegetable, flowering
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_plants_by_category",
			Description: "Get all plants in a category. Category must be one of: 'tropical', 'succulent', 'herb', 'vegetable', 'flowering'. Returns plant names with key stats.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_plants_by_category called: category=%s", input.Category)

			cat := strings.ToLower(input.Category)
			catName, ok := plantCategories[cat]
			if !ok {
				return Output{Result: fmt.Sprintf("Unknown category '%s'. Available: tropical, succulent, herb, vegetable, flowering.", input.Category)}, nil
			}

			var plants []PlantSpecies
			for _, p := range plantDatabase {
				if p.Category == cat {
					plants = append(plants, p)
				}
			}

			result := fmt.Sprintf("%s (%d species):\n\n", catName, len(plants))
			for _, p := range plants {
				result += fmt.Sprintf("  %s (%s)\n", p.Name, p.Scientific)
				result += fmt.Sprintf("    Moisture: %d-%d%% | Temp: %.0f-%.0f C | %s | %s\n",
					p.HumidityMin, p.HumidityMax, p.TempMin, p.TempMax, p.Light, p.Difficulty)
			}

			return Output{Result: result}, nil
		},
	)
}

// createGetPlantCareTool — detailed care info for a specific plant
func createGetPlantCareTool() (tool.Tool, error) {
	type Input struct {
		PlantName string `json:"plantName"`
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "get_plant_care",
			Description: "Get detailed care guide for a specific plant: humidity thresholds for Zefir sensor, temperature range, light needs, watering schedule. Use when user asks 'how to care for monstera', 'what humidity does aloe need', 'watering schedule for basil'.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] get_plant_care called: plantName=%s", input.PlantName)

			if input.PlantName == "" {
				return Output{Result: "Error: plant name is required"}, nil
			}

			query := strings.ToLower(input.PlantName)
			var found *PlantSpecies

			for i, p := range plantDatabase {
				if strings.EqualFold(p.Name, input.PlantName) ||
					strings.EqualFold(p.NameRu, input.PlantName) ||
					strings.EqualFold(p.NameDe, input.PlantName) ||
					strings.EqualFold(p.Scientific, input.PlantName) ||
					strings.Contains(strings.ToLower(p.Name), query) ||
					strings.Contains(strings.ToLower(p.NameRu), query) {
					found = &plantDatabase[i]
					break
				}
			}

			if found == nil {
				return Output{Result: fmt.Sprintf("Plant '%s' not found. Try search_plant(query) to find it.", input.PlantName)}, nil
			}

			result := formatPlantCareGuide(*found)
			return Output{Result: result}, nil
		},
	)
}

// createComparePlantsTool — side-by-side comparison
func createComparePlantsTool() (tool.Tool, error) {
	type Input struct {
		Plants []string `json:"plants"` // 2-4 plant names
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "compare_plants",
			Description: "Compare 2-4 plants side by side: humidity ranges, temperature, light, difficulty. Use when user asks 'compare monstera and pothos', 'which needs more water, aloe or cactus'.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] compare_plants called: plants=%v", input.Plants)

			if len(input.Plants) < 2 {
				return Output{Result: "Need at least 2 plants to compare."}, nil
			}
			if len(input.Plants) > 4 {
				return Output{Result: "Can compare maximum 4 plants at once."}, nil
			}

			var found []PlantSpecies
			var notFound []string

			for _, name := range input.Plants {
				p := findPlant(name)
				if p != nil {
					found = append(found, *p)
				} else {
					notFound = append(notFound, name)
				}
			}

			if len(found) < 2 {
				return Output{Result: fmt.Sprintf("Not enough plants found for comparison. Not found: %s", strings.Join(notFound, ", "))}, nil
			}

			result := fmt.Sprintf("COMPARISON (%d plants):\n\n", len(found))
			result += fmt.Sprintf("%-20s", "")
			for _, p := range found {
				result += fmt.Sprintf("| %-20s", p.Name)
			}
			result += "\n" + strings.Repeat("-", 20+22*len(found)) + "\n"

			result += fmt.Sprintf("%-20s", "Moisture %")
			for _, p := range found {
				result += fmt.Sprintf("| %-20s", fmt.Sprintf("%d-%d%%", p.HumidityMin, p.HumidityMax))
			}
			result += "\n"

			result += fmt.Sprintf("%-20s", "Temperature")
			for _, p := range found {
				result += fmt.Sprintf("| %-20s", fmt.Sprintf("%.0f-%.0f C", p.TempMin, p.TempMax))
			}
			result += "\n"

			result += fmt.Sprintf("%-20s", "Light")
			for _, p := range found {
				result += fmt.Sprintf("| %-20s", p.Light)
			}
			result += "\n"

			result += fmt.Sprintf("%-20s", "Watering")
			for _, p := range found {
				w := p.Watering
				if len(w) > 18 {
					w = w[:18] + ".."
				}
				result += fmt.Sprintf("| %-20s", w)
			}
			result += "\n"

			result += fmt.Sprintf("%-20s", "Difficulty")
			for _, p := range found {
				result += fmt.Sprintf("| %-20s", p.Difficulty)
			}
			result += "\n"

			if len(notFound) > 0 {
				result += fmt.Sprintf("\nNot found: %s", strings.Join(notFound, ", "))
			}

			return Output{Result: result}, nil
		},
	)
}

// createRecommendPlantsTool — recommend plants by criteria
func createRecommendPlantsTool() (tool.Tool, error) {
	type Input struct {
		Criteria string `json:"criteria"` // beginner, edible, tropical, low-light, drought-tolerant, flowering, etc.
	}
	type Output struct {
		Result string `json:"result"`
	}

	return functiontool.New(
		functiontool.Config{
			Name:        "recommend_plants",
			Description: "Recommend plants by criteria: 'beginner' (easy care), 'edible' (herbs/vegs), 'tropical', 'low-light', 'drought-tolerant', 'flowering', 'air-purifier', 'compact', 'pet-safe'. Use when user asks 'suggest easy plants', 'best plants for beginners', 'what can I eat'.",
		},
		func(ctx tool.Context, input Input) (Output, error) {
			log.Printf("[TOOL] recommend_plants called: criteria=%s", input.Criteria)

			if input.Criteria == "" {
				return Output{Result: "Error: criteria is required. Examples: beginner, edible, tropical, low-light, drought-tolerant, flowering."}, nil
			}

			criteria := strings.ToLower(input.Criteria)
			var matches []PlantSpecies

			for _, p := range plantDatabase {
				if matchesCriteria(p, criteria) {
					matches = append(matches, p)
				}
			}

			if len(matches) == 0 {
				return Output{Result: fmt.Sprintf("No plants match criteria '%s'. Try: beginner, edible, tropical, low-light, drought-tolerant, flowering.", input.Criteria)}, nil
			}

			if len(matches) > 10 {
				matches = matches[:10]
			}

			result := fmt.Sprintf("Recommended plants for '%s' (%d results):\n\n", input.Criteria, len(matches))
			for i, p := range matches {
				result += fmt.Sprintf("%d. %s (%s)\n", i+1, p.Name, p.NameRu)
				result += fmt.Sprintf("   Moisture: %d-%d%% | %s | %s\n",
					p.HumidityMin, p.HumidityMax, p.Light, p.Difficulty)
				if p.CareNotes != "" {
					note := p.CareNotes
					if len(note) > 80 {
						note = note[:80] + "..."
					}
					result += fmt.Sprintf("   Tip: %s\n", note)
				}
				result += "\n"
			}

			return Output{Result: result}, nil
		},
	)
}

// ============================================================================
// Helper functions
// ============================================================================

func matchesPlant(p PlantSpecies, query string) bool {
	if strings.Contains(strings.ToLower(p.Name), query) ||
		strings.Contains(strings.ToLower(p.NameRu), query) ||
		strings.Contains(strings.ToLower(p.NameDe), query) ||
		strings.Contains(strings.ToLower(p.Scientific), query) {
		return true
	}
	for _, tag := range p.Tags {
		if strings.Contains(tag, query) {
			return true
		}
	}
	return false
}

func matchesCriteria(p PlantSpecies, criteria string) bool {
	switch {
	case strings.Contains(criteria, "beginner") || strings.Contains(criteria, "easy"):
		return p.Difficulty == "beginner"
	case strings.Contains(criteria, "edible") || strings.Contains(criteria, "eat") || strings.Contains(criteria, "food"):
		return p.Edible
	case strings.Contains(criteria, "tropical"):
		return p.Category == "tropical"
	case strings.Contains(criteria, "succulent") || strings.Contains(criteria, "cactus"):
		return p.Category == "succulent"
	case strings.Contains(criteria, "herb"):
		return p.Category == "herb"
	case strings.Contains(criteria, "vegetable"):
		return p.Category == "vegetable"
	case strings.Contains(criteria, "flowering") || strings.Contains(criteria, "flower"):
		return p.Category == "flowering"
	case strings.Contains(criteria, "low-light") || strings.Contains(criteria, "shade") || strings.Contains(criteria, "dark"):
		return p.Light == "low" || p.Light == "partial"
	case strings.Contains(criteria, "drought") || strings.Contains(criteria, "dry"):
		return p.HumidityMax <= 40
	case strings.Contains(criteria, "air-purif"):
		for _, tag := range p.Tags {
			if tag == "air-purifier" {
				return true
			}
		}
		return false
	case strings.Contains(criteria, "compact") || strings.Contains(criteria, "small"):
		for _, tag := range p.Tags {
			if tag == "compact" || tag == "windowsill" {
				return true
			}
		}
		return false
	default:
		// Generic tag search
		for _, tag := range p.Tags {
			if strings.Contains(tag, criteria) {
				return true
			}
		}
		return p.Category == criteria
	}
}

func findPlant(name string) *PlantSpecies {
	query := strings.ToLower(name)
	for i, p := range plantDatabase {
		if strings.EqualFold(p.Name, name) ||
			strings.EqualFold(p.NameRu, name) ||
			strings.Contains(strings.ToLower(p.Name), query) ||
			strings.Contains(strings.ToLower(p.NameRu), query) {
			return &plantDatabase[i]
		}
	}
	return nil
}

func formatPlantSummary(p PlantSpecies) string {
	edible := ""
	if p.Edible {
		edible = " [edible]"
	}
	return fmt.Sprintf("  %s (%s)%s\n  Category: %s | Difficulty: %s\n  Moisture: %d-%d%% | Temp: %.0f-%.0f C | Light: %s\n\n",
		p.Name, p.NameRu, edible,
		plantCategories[p.Category], p.Difficulty,
		p.HumidityMin, p.HumidityMax, p.TempMin, p.TempMax, p.Light)
}

func formatPlantCareGuide(p PlantSpecies) string {
	result := fmt.Sprintf("PLANT CARE GUIDE: %s\n", p.Name)
	result += fmt.Sprintf("Scientific: %s\n", p.Scientific)
	result += fmt.Sprintf("Names: EN: %s | RU: %s | DE: %s\n", p.Name, p.NameRu, p.NameDe)
	result += fmt.Sprintf("Category: %s | Difficulty: %s\n\n", plantCategories[p.Category], p.Difficulty)

	result += "ZEFIR SENSOR THRESHOLDS:\n"
	result += fmt.Sprintf("  Soil Moisture: %d%% - %d%%\n", p.HumidityMin, p.HumidityMax)
	result += fmt.Sprintf("  Temperature: %.0f C - %.0f C\n", p.TempMin, p.TempMax)
	result += fmt.Sprintf("  Light: %s\n\n", p.Light)

	result += "WATERING:\n"
	result += fmt.Sprintf("  %s\n\n", p.Watering)

	if p.Edible {
		result += "EDIBLE: Yes\n\n"
	}

	result += "CARE NOTES:\n"
	result += fmt.Sprintf("  %s\n\n", p.CareNotes)

	if len(p.Tags) > 0 {
		result += fmt.Sprintf("Tags: %s\n", strings.Join(p.Tags, ", "))
	}

	return result
}
