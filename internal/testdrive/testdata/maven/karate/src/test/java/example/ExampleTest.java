package example;
public class ExampleTest {
    @com.intuit.karate.junit5.Karate.Test com.intuit.karate.junit5.Karate works() {
        return com.intuit.karate.junit5.Karate.run("example").relativeTo(getClass());
    }
}
