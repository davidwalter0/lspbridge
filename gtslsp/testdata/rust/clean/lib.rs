use std::collections::HashMap;

/// A point in the plane.
pub struct Point {
    pub x: i32,
    pub y: i32,
}

pub enum Shape {
    Circle(f64),
    Square(f64),
}

pub trait Area {
    fn area(&self) -> f64;
}

impl Area for Shape {
    fn area(&self) -> f64 {
        match self {
            Shape::Circle(r) => 3.14 * r * r,
            Shape::Square(s) => s * s,
        }
    }
}

mod util {
    pub fn double(n: i32) -> i32 {
        n * 2
    }
}

pub fn origin() -> Point {
    let mut m: HashMap<String, i32> = HashMap::new();
    m.insert("a".to_string(), util::double(1));
    Point { x: 0, y: 0 }
}
