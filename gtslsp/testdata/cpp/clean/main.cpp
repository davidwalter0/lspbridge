#include <string>
#include <vector>

namespace shapes {

class Shape {
public:
    virtual ~Shape() = default;
    virtual double area() const = 0;
};

struct Circle : public Shape {
    double r;
    explicit Circle(double radius) : r(radius) {}
    double area() const override { return 3.14159 * r * r; }
};

template <typename T>
T twice(T v)
{
    return v + v;
}

}  // namespace shapes

int total(const std::vector<shapes::Circle> &cs)
{
    struct shapes::Circle first = cs[0];
    double sum = 0;
    for (const auto &c : cs) {
        sum += c.area();
    }
    return static_cast<int>(sum + first.r);
}
