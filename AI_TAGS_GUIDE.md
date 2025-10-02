# Руководство по добавлению AI-тегов в HTML магазина

## Зачем нужны `data-ai-*` атрибуты?

AI-ассистент (Gemini) может инспектировать страницы вашего магазина, чтобы помогать клиентам с вопросами об интерфейсе. Добавление специальных `data-ai-*` атрибутов помогает AI точно понимать как работает сайт.

---

## Доступные атрибуты

### 1. `data-ai-page` - описание страницы
Добавляется к основному контейнеру страницы.

```html
<div data-ai-page="home" data-ai-help="Main page with product catalog">
  <!-- содержимое главной страницы -->
</div>

<div data-ai-page="cart" data-ai-help="Shopping cart page - view and manage items">
  <!-- корзина -->
</div>

<div data-ai-page="checkout" data-ai-help="Complete your order here">
  <!-- оформление заказа -->
</div>
```

### 2. `data-ai-element` - название UI элемента
Для важных элементов интерфейса.

```html
<div data-ai-element="search-bar" data-ai-help="Type product name to search">
  <input type="search" placeholder="Search products...">
</div>

<nav data-ai-element="category-navigation" data-ai-help="Click category to browse products">
  <a href="/category/fish">Лосось</a>
  <a href="/category/fresh">Свежие продукты</a>
</nav>

<div data-ai-element="product-grid" data-ai-help="All available products">
  <!-- список товаров -->
</div>
```

### 3. `data-ai-action` - действие которое можно выполнить
Для кнопок и интерактивных элементов.

```html
<button data-ai-action="add-to-cart" data-ai-help="Add this product to cart">
  Add to Cart
</button>

<button data-ai-action="update-quantity" data-ai-help="Change product quantity">
  +/-
</button>

<button data-ai-action="remove-item" data-ai-help="Remove product from cart">
  Remove
</button>

<button data-ai-action="checkout" data-ai-location="bottom-right" data-ai-help="Proceed to checkout page">
  Checkout
</button>

<button data-ai-action="place-order" data-ai-help="Complete and submit your order">
  Place Order
</button>
```

### 4. `data-ai-step` - шаг в процессе
Для многошаговых форм (например, оформление заказа).

```html
<form data-ai-page="checkout">
  <div data-ai-step="1" data-ai-help="Enter delivery address: street, building, apartment">
    <input name="street" placeholder="Street">
    <input name="building" placeholder="Building">
    <input name="apartment" placeholder="Apartment">
  </div>

  <div data-ai-step="2" data-ai-help="Choose preferred delivery time">
    <select name="delivery_time">
      <option>Today 18:00-20:00</option>
      <option>Tomorrow 10:00-12:00</option>
    </select>
  </div>

  <div data-ai-step="3" data-ai-help="Select payment method: card, cash, or Apple Pay">
    <label><input type="radio" name="payment" value="card"> Card</label>
    <label><input type="radio" name="payment" value="cash"> Cash</label>
    <label><input type="radio" name="payment" value="applepay"> Apple Pay</label>
  </div>

  <button data-ai-step="4" data-ai-action="place-order">Place Order</button>
</form>
```

### 5. `data-ai-location` - расположение элемента
Помогает AI объяснить где находится элемент.

```html
<div data-ai-element="search-bar" data-ai-location="header, top-right">
  <input type="search">
</div>

<nav data-ai-element="main-menu" data-ai-location="top navigation bar">
  <a href="/">Home</a>
  <a href="/cart">Cart</a>
  <a href="/profile">Profile</a>
</nav>

<button data-ai-action="checkout" data-ai-location="bottom of cart page">
  Proceed to Checkout
</button>
```

### 6. `data-ai-help` - инструкция для AI
Основной атрибут - как AI должна объяснить это клиенту.

```html
<button data-ai-action="filter-products"
        data-ai-help="Click to filter products by category, price or availability">
  Filters
</button>

<div data-ai-element="product-card"
     data-ai-help="Each card shows product name, price and 'Add to Cart' button">
  <!-- карточка товара -->
</div>
```

---

## Примеры для разных страниц

### Главная страница (Home)

```html
<div data-ai-page="home" data-ai-help="Product catalog main page">

  <!-- Поиск -->
  <div data-ai-element="search" data-ai-location="top-right header"
       data-ai-help="Type product name and press Enter to search">
    <input type="search" placeholder="Search products...">
  </div>

  <!-- Категории -->
  <nav data-ai-element="categories" data-ai-location="left sidebar"
       data-ai-help="Click category to see products in that category">
    <a href="/category/fish" data-ai-action="browse-category">Лосось</a>
    <a href="/category/fresh" data-ai-action="browse-category">Свежие продукты</a>
    <a href="/category/rolls" data-ai-action="browse-category">Роллы</a>
  </nav>

  <!-- Товары -->
  <div data-ai-element="product-grid">
    <div class="product-card" data-ai-element="product"
         data-ai-help="Product card with image, name, price and 'Add to Cart' button">
      <h3>Филе лосося</h3>
      <p>9.00₾</p>
      <button data-ai-action="add-to-cart" data-ai-help="Click to add to cart">
        Add to Cart
      </button>
    </div>
  </div>
</div>
```

### Корзина (Cart)

```html
<div data-ai-page="cart" data-ai-help="Shopping cart - view and manage your items">

  <div class="cart-items">
    <div class="cart-item" data-ai-element="cart-item">
      <h4>Филе лосося</h4>

      <div data-ai-element="quantity-controls"
           data-ai-help="Use + and - buttons to change quantity">
        <button data-ai-action="decrease-quantity">-</button>
        <span>2</span>
        <button data-ai-action="increase-quantity">+</button>
      </div>

      <button data-ai-action="remove-from-cart"
              data-ai-help="Click X to remove item from cart">
        ✕
      </button>
    </div>
  </div>

  <div data-ai-element="cart-total" data-ai-help="Total price of all items">
    Total: 18.00₾
  </div>

  <button data-ai-action="checkout" data-ai-location="bottom-right"
          data-ai-help="Click to proceed to checkout and complete order">
    Proceed to Checkout
  </button>
</div>
```

### Оформление заказа (Checkout)

```html
<form data-ai-page="checkout" data-ai-help="Complete order form with 3 steps">

  <!-- Шаг 1: Адрес -->
  <div data-ai-step="1" data-ai-help="Enter full delivery address">
    <h3>Delivery Address</h3>
    <input name="street" placeholder="Street"
           data-ai-help="Enter street name">
    <input name="building" placeholder="Building number">
    <input name="apartment" placeholder="Apartment number">
  </div>

  <!-- Шаг 2: Время доставки -->
  <div data-ai-step="2" data-ai-help="Select convenient delivery time slot">
    <h3>Delivery Time</h3>
    <select name="delivery_time" data-ai-help="Choose from available time slots">
      <option>Today 18:00-20:00</option>
      <option>Tomorrow 10:00-12:00</option>
      <option>Tomorrow 14:00-16:00</option>
    </select>
  </div>

  <!-- Шаг 3: Оплата -->
  <div data-ai-step="3" data-ai-help="Choose payment method">
    <h3>Payment Method</h3>
    <label data-ai-help="Pay with bank card online">
      <input type="radio" name="payment" value="card"> Card
    </label>
    <label data-ai-help="Pay cash to courier on delivery">
      <input type="radio" name="payment" value="cash"> Cash
    </label>
    <label data-ai-help="Pay with Apple Pay">
      <input type="radio" name="payment" value="applepay"> Apple Pay
    </label>
  </div>

  <!-- Кнопка заказа -->
  <button type="submit" data-ai-action="place-order"
          data-ai-help="Click to confirm and place your order">
    Place Order
  </button>
</form>
```

### Регистрация / Вход

```html
<div data-ai-page="auth" data-ai-help="Login or register page">

  <form data-ai-element="login-form" data-ai-help="Login with existing account">
    <h2>Login</h2>
    <input type="email" data-ai-help="Enter your registered email">
    <input type="password" data-ai-help="Enter your password">
    <button data-ai-action="login">Login</button>
  </form>

  <form data-ai-element="register-form" data-ai-help="Create new account">
    <h2>Register</h2>
    <input type="email" data-ai-help="Enter email for new account">
    <input type="password" data-ai-help="Create password (min 6 characters)">
    <button data-ai-action="register">Sign Up</button>
  </form>
</div>
```

### Личный кабинет / Заказы

```html
<div data-ai-page="my-orders" data-ai-help="View your order history and status">

  <div class="order" data-ai-element="order-item">
    <h3>Order #12345</h3>
    <p data-ai-element="order-status" data-ai-help="Current order status">
      Status: In Delivery
    </p>
    <button data-ai-action="track-order"
            data-ai-help="Click to see order details and courier location">
      Track Order
    </button>
  </div>
</div>
```

---

## Рекомендации

1. **Добавляйте на ключевые элементы**: не нужно помечать каждый div, только важные для взаимодействия

2. **Пишите понятно**: `data-ai-help` должен объяснять что делать, как в инструкции для пользователя

3. **Используйте комбинации**:
   - `data-ai-element` + `data-ai-help` - для описания элемента
   - `data-ai-action` + `data-ai-help` - для кнопок и действий
   - `data-ai-step` + `data-ai-help` - для многошаговых форм

4. **Локализация**: Если сайт многоязычный, можно добавить `data-ai-help-ru`, `data-ai-help-en` и т.д.

5. **SEO-безопасно**: `data-*` атрибуты не влияют на SEO, но могут использоваться в микроразметке

---

## Что получит AI

После добавления тегов, когда клиент спросит **"Как мне оформить заказ?"**, AI вызовет `inspect_website_page("/checkout")` и получит:

```
🤖 AI Instructions (from website):
  • [PAGE: checkout] Complete order form with 3 steps
  • Step 1: Enter full delivery address
  • Step 2: Select convenient delivery time slot
  • Step 3: Choose payment method
  • place-order - Click to confirm and place your order
```

И сможет дать точный ответ клиенту:

> "Для оформления заказа перейдите на страницу checkout. Там нужно:
> 1. Указать адрес доставки (улица, дом, квартира)
> 2. Выбрать удобное время доставки
> 3. Выбрать способ оплаты (карта, наличные или Apple Pay)
> 4. Нажать кнопку 'Place Order' для подтверждения"

---

## Начните с главной страницы

Добавьте теги на:
1. ✅ Поиск товаров
2. ✅ Категории / навигация
3. ✅ Кнопка "Add to Cart"
4. ✅ Кнопка "Checkout"
5. ✅ Форма оформления заказа

Даже эти 5 элементов резко улучшат качество ответов AI!
